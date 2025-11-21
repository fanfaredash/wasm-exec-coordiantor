// SPDX-License-Identifier: MIT
pragma solidity ^0.8.20;

interface IVerificationPrecompile {
    // Returns true if the snapshot passes verification, false if invalid (challenge succeeds on false).
    function verify(bytes calldata snapshotCID, bytes32 leafHash) external view returns (bool ok);
}

/// @title ComputeMarket
/// @notice Compute marketplace + dispute layer. Clusters list resources, users publish IPFS
///         tasks with payment escrowed into a per-task pool. Results carry snapshot + Merkle
///         root for existence proofs and stay in a 3-day challenge window. Challengers can
///         stake and dispute via a verification precompile; successful challenges win the pool,
///         failed challenges forfeit stake to the pool. Clusters withdraw earnings after a
///         clean challenge window.
contract ComputeMarket {
    enum TaskStatus {
        Published,
        Accepted,
        ResultProposed,
        Settled,
        Failed,
        Cancelled
    }

    struct Cluster {
        address owner;
        string name;
        string metadataCID; // optional IPFS metadata for the cluster
        bool active;
        uint256 balance; // earnings available for withdrawal
    }

    struct Resource {
        uint256 clusterId;
        string name;
        string specCID; // IPFS CID describing hardware/benchmark profile
        uint256 priceWei; // fixed price per task in native token
        bool active;
    }

    struct Task {
        uint256 resourceId;
        address user;
        string inputCID; // IPFS CID of input payload
        string entry; // optional entry/function hint for the executor
        TaskStatus status;
        string resultCID; // filled on result proposal
        string snapshotCID; // IPFS CID of intermediate snapshot
        bytes32 snapshotRoot; // Merkle root committing to snapshot set
        string failReasonCID; // optional IPFS CID describing failure reason/logs
        uint256 pricePaid; // escrowed amount (pool) used for payouts
        uint256 challengeDeadline; // timestamp result can be challenged until
    }

    uint256 public clusterCount;
    uint256 public resourceCount;
    uint256 public taskCount;

    mapping(uint256 => Cluster) public clusters;
    mapping(uint256 => Resource) public resources;
    mapping(uint256 => Task) public tasks;
    mapping(uint256 => mapping(address => uint256)) public challengerStake; // taskId => challenger => stake

    bool private locked;

    uint256 public constant CHALLENGE_WINDOW = 3 days;
    address public immutable verificationPrecompile;

    // --- Events ---
    event ClusterRegistered(uint256 indexed clusterId, address indexed owner, string name);
    event ClusterUpdated(uint256 indexed clusterId, string name, string metadataCID, bool active);
    event ResourceRegistered(uint256 indexed resourceId, uint256 indexed clusterId, string name, uint256 priceWei);
    event ResourceUpdated(uint256 indexed resourceId, string name, string specCID, uint256 priceWei, bool active);
    event TaskPublished(uint256 indexed taskId, uint256 indexed resourceId, address indexed user, string inputCID, string entry);
    event TaskAccepted(uint256 indexed taskId, uint256 indexed clusterId);
    event TaskResultProposed(
        uint256 indexed taskId,
        uint256 indexed clusterId,
        string resultCID,
        string snapshotCID,
        bytes32 snapshotRoot,
        uint256 challengeDeadline
    );
    event TaskSettled(uint256 indexed taskId, uint256 indexed clusterId, address receiver, uint256 amount);
    event TaskFailed(uint256 indexed taskId, uint256 indexed clusterId, string reasonCID);
    event TaskCancelled(uint256 indexed taskId);
    event TaskChallenged(uint256 indexed taskId, address indexed challenger, uint256 stake, bytes32 leaf, bool success);
    event ClusterWithdraw(uint256 indexed clusterId, address indexed to, uint256 amount);

    // --- Modifiers ---
    modifier nonReentrant() {
        require(!locked, "reentrancy");
        locked = true;
        _;
        locked = false;
    }

    modifier onlyClusterOwner(uint256 clusterId) {
        require(clusters[clusterId].owner == msg.sender, "not cluster owner");
        _;
    }

    modifier taskExists(uint256 taskId) {
        require(taskId > 0 && taskId <= taskCount, "task not found");
        _;
    }

    modifier resourceExists(uint256 resourceId) {
        require(resourceId > 0 && resourceId <= resourceCount, "resource not found");
        _;
    }

    // --- Constructor ---
    constructor(address precompile) {
        require(precompile != address(0), "precompile required");
        verificationPrecompile = precompile;
    }

    // --- Cluster management ---
    function registerCluster(string calldata name, string calldata metadataCID) external returns (uint256 clusterId) {
        clusterId = ++clusterCount;
        clusters[clusterId] = Cluster({
            owner: msg.sender,
            name: name,
            metadataCID: metadataCID,
            active: true,
            balance: 0
        });
        emit ClusterRegistered(clusterId, msg.sender, name);
    }

    function updateCluster(
        uint256 clusterId,
        string calldata name,
        string calldata metadataCID,
        bool active
    ) external onlyClusterOwner(clusterId) {
        Cluster storage c = clusters[clusterId];
        c.name = name;
        c.metadataCID = metadataCID;
        c.active = active;
        emit ClusterUpdated(clusterId, name, metadataCID, active);
    }

    // --- Resource management ---
    function registerResource(
        uint256 clusterId,
        string calldata name,
        string calldata specCID,
        uint256 priceWei
    ) external onlyClusterOwner(clusterId) returns (uint256 resourceId) {
        require(clusters[clusterId].active, "cluster inactive");
        require(priceWei > 0, "price must be > 0");

        resourceId = ++resourceCount;
        resources[resourceId] = Resource({
            clusterId: clusterId,
            name: name,
            specCID: specCID,
            priceWei: priceWei,
            active: true
        });
        emit ResourceRegistered(resourceId, clusterId, name, priceWei);
    }

    function updateResource(
        uint256 resourceId,
        string calldata name,
        string calldata specCID,
        uint256 priceWei,
        bool active
    ) external onlyClusterOwner(resources[resourceId].clusterId) resourceExists(resourceId) {
        require(priceWei > 0, "price must be > 0");

        Resource storage r = resources[resourceId];
        r.name = name;
        r.specCID = specCID;
        r.priceWei = priceWei;
        r.active = active;

        emit ResourceUpdated(resourceId, name, specCID, priceWei, active);
    }

    // --- Task lifecycle ---
    function publishTask(
        uint256 resourceId,
        string calldata inputCID,
        string calldata entry
    ) external payable resourceExists(resourceId) returns (uint256 taskId) {
        Resource memory r = resources[resourceId];
        require(r.active, "resource inactive");
        require(clusters[r.clusterId].active, "cluster inactive");
        require(msg.value == r.priceWei, "incorrect payment");

        taskId = ++taskCount;
        tasks[taskId] = Task({
            resourceId: resourceId,
            user: msg.sender,
            inputCID: inputCID,
            entry: entry,
            status: TaskStatus.Published,
            resultCID: "",
            snapshotCID: "",
            snapshotRoot: bytes32(0),
            failReasonCID: "",
            pricePaid: msg.value,
            challengeDeadline: 0
        });

        emit TaskPublished(taskId, resourceId, msg.sender, inputCID, entry);
    }

    function acceptTask(uint256 taskId) external taskExists(taskId) {
        Task storage t = tasks[taskId];
        Resource memory r = resources[t.resourceId];
        require(msg.sender == clusters[r.clusterId].owner, "not cluster owner");
        require(clusters[r.clusterId].active && r.active, "resource unavailable");
        require(t.status == TaskStatus.Published, "bad status");

        t.status = TaskStatus.Accepted;
        emit TaskAccepted(taskId, r.clusterId);
    }

    function submitResult(
        uint256 taskId,
        string calldata resultCID,
        string calldata snapshotCID,
        bytes32 snapshotRoot
    ) external taskExists(taskId) nonReentrant {
        Task storage t = tasks[taskId];
        Resource memory r = resources[t.resourceId];
        Cluster storage c = clusters[r.clusterId];

        require(msg.sender == c.owner, "not cluster owner");
        require(t.status == TaskStatus.Accepted, "bad status");
        require(bytes(resultCID).length > 0, "empty result");
        require(bytes(snapshotCID).length > 0, "empty snapshot");
        require(snapshotRoot != bytes32(0), "empty snapshot root");

        t.status = TaskStatus.ResultProposed;
        t.resultCID = resultCID;
        t.snapshotCID = snapshotCID;
        t.snapshotRoot = snapshotRoot;
        t.challengeDeadline = block.timestamp + CHALLENGE_WINDOW;

        emit TaskResultProposed(taskId, r.clusterId, resultCID, snapshotCID, snapshotRoot, t.challengeDeadline);
    }

    function failTask(uint256 taskId, string calldata reasonCID) external taskExists(taskId) nonReentrant {
        Task storage t = tasks[taskId];
        Resource memory r = resources[t.resourceId];
        Cluster storage c = clusters[r.clusterId];

        require(msg.sender == c.owner, "not cluster owner");
        require(t.status == TaskStatus.Accepted || t.status == TaskStatus.Published, "bad status");

        t.status = TaskStatus.Failed;
        t.failReasonCID = reasonCID;

        uint256 refund = t.pricePaid;
        t.pricePaid = 0;
        if (refund > 0) {
            (bool ok, ) = t.user.call{value: refund}("");
            require(ok, "refund failed");
        }
        emit TaskFailed(taskId, r.clusterId, reasonCID);
    }

    function cancelTask(uint256 taskId) external taskExists(taskId) nonReentrant {
        Task storage t = tasks[taskId];
        require(t.user == msg.sender, "not task owner");
        require(t.status == TaskStatus.Published, "cannot cancel");

        t.status = TaskStatus.Cancelled;
        uint256 refund = t.pricePaid;
        t.pricePaid = 0;
        if (refund > 0) {
            (bool ok, ) = msg.sender.call{value: refund}("");
            require(ok, "refund failed");
        }
        emit TaskCancelled(taskId);
    }

    // --- Challenges & settlement ---
    function _verifyProof(bytes32 leaf, bytes32 root, bytes32[] memory proof) internal pure returns (bool) {
        bytes32 computed = leaf;
        for (uint256 i = 0; i < proof.length; i++) {
            bytes32 sibling = proof[i];
            if (computed <= sibling) {
                computed = keccak256(abi.encodePacked(computed, sibling));
            } else {
                computed = keccak256(abi.encodePacked(sibling, computed));
            }
        }
        return computed == root;
    }

    function challengeTask(
        uint256 taskId,
        bytes32 snapshotLeaf,
        bytes32[] calldata merkleProof
    ) external payable taskExists(taskId) nonReentrant {
        Task storage t = tasks[taskId];
        Resource memory r = resources[t.resourceId];
        Cluster storage c = clusters[r.clusterId];

        require(t.status == TaskStatus.ResultProposed, "not challengeable");
        require(block.timestamp <= t.challengeDeadline, "window closed");
        require(msg.value > 0, "stake required");
        require(_verifyProof(snapshotLeaf, t.snapshotRoot, merkleProof), "invalid proof");

        challengerStake[taskId][msg.sender] += msg.value;

        bool ok = IVerificationPrecompile(verificationPrecompile).verify(
            bytes(t.snapshotCID),
            snapshotLeaf
        );

        if (ok) {
            // challenge failed: stake flows into pool for the cluster
            t.pricePaid += msg.value;
            emit TaskChallenged(taskId, msg.sender, msg.value, snapshotLeaf, false);
        } else {
            // challenge succeeded: challenger wins pool + its stake
            t.status = TaskStatus.Failed;
            uint256 payout = t.pricePaid + msg.value;
            t.pricePaid = 0;
            // zero our stake to avoid double accounting
            challengerStake[taskId][msg.sender] = 0;
            (bool sent, ) = msg.sender.call{value: payout}("");
            require(sent, "payout failed");
            emit TaskChallenged(taskId, msg.sender, msg.value, snapshotLeaf, true);
        }
    }

    function settleTask(uint256 taskId) external taskExists(taskId) nonReentrant {
        Task storage t = tasks[taskId];
        Resource memory r = resources[t.resourceId];
        Cluster storage c = clusters[r.clusterId];

        require(t.status == TaskStatus.ResultProposed, "not settleable");
        require(block.timestamp > t.challengeDeadline, "challenge window open");

        t.status = TaskStatus.Settled;
        uint256 amount = t.pricePaid;
        t.pricePaid = 0;
        c.balance += amount;
        emit TaskSettled(taskId, r.clusterId, c.owner, amount);
    }

    // --- Withdrawals ---
    function withdrawCluster(uint256 clusterId, address payable to, uint256 amount)
        external
        onlyClusterOwner(clusterId)
        nonReentrant
    {
        Cluster storage c = clusters[clusterId];
        require(amount > 0 && amount <= c.balance, "invalid amount");

        c.balance -= amount;
        (bool ok, ) = to.call{value: amount}("");
        require(ok, "withdraw failed");

        emit ClusterWithdraw(clusterId, to, amount);
    }

    // --- Views ---
    function getCluster(uint256 clusterId) external view returns (Cluster memory) {
        return clusters[clusterId];
    }

    function getResource(uint256 resourceId) external view returns (Resource memory) {
        return resources[resourceId];
    }

    function getTask(uint256 taskId) external view returns (Task memory) {
        return tasks[taskId];
    }
}
