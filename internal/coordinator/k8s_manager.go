package coordinator

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/apimachinery/pkg/util/wait"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"sigs.k8s.io/yaml"
)

// KubeManager 负责与 Kubernetes API 交互，贯穿任务创建、监控与清理。
type KubeManager struct {
	client    *kubernetes.Clientset
	namespace string
	log       Logger
	template  *batchv1.Job
}

// NewKubeManager 优先使用集群内配置，失败时回退到本地 kubeconfig。
func NewKubeManager(namespace string, log Logger) (*KubeManager, error) {
	cfg, err := rest.InClusterConfig()
	if err != nil {
		cfg, err = clientcmd.BuildConfigFromFlags("", clientcmd.RecommendedHomeFile)
		if err != nil {
			return nil, fmt.Errorf("build kube config: %w", err)
		}
	}

	cs, err := kubernetes.NewForConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("build clientset: %w", err)
	}

	return &KubeManager{
		client:    cs,
		namespace: namespace,
		log:       defaultLogger(log),
	}, nil
}

// LoadTemplate 读取 Job 模板并缓存，后续任务可直接复用骨架。
func (m *KubeManager) LoadTemplate(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read job template: %w", err)
	}
	var job batchv1.Job
	if err := yaml.Unmarshal(data, &job); err != nil {
		return fmt.Errorf("unmarshal job template: %w", err)
	}
	m.template = job.DeepCopy()
	m.log.Infof("loaded job template from %s", path)
	return nil
}

// CreateJob 将 Wasm/输入写入 ConfigMap，并基于模板创建一次性 Job。
func (m *KubeManager) CreateJob(ctx context.Context, cfg Config, task TaskRequest, wasm []byte) (string, []string, error) {
	if m.template == nil {
		return "", nil, fmt.Errorf("job template not loaded")
	}

	jobName := m.jobName(task.TaskID)
	var configMaps []string

	moduleCM := m.configMapName(task.TaskID)
	m.log.Infof("task %s: creating module configmap %s", task.TaskID, moduleCM)
	cm := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      moduleCM,
			Namespace: m.namespace,
			Labels: map[string]string{
				labelManagedBy: controllerName,
				labelTaskID:    task.TaskID,
			},
		},
		BinaryData: map[string][]byte{
			wasmFileName: wasm,
		},
	}
	if _, err := m.client.CoreV1().ConfigMaps(m.namespace).Create(ctx, cm, metav1.CreateOptions{}); err != nil {
		m.log.Errorf("task %s: create module configmap failed: %v", task.TaskID, err)
		return "", nil, fmt.Errorf("create module configmap: %w", err)
	}
	configMaps = append(configMaps, moduleCM)

	var inputCM string
	if len(task.InputJSON) > 0 {
		inputCM = m.inputConfigMapName(task.TaskID)
		m.log.Infof("task %s: creating input configmap %s", task.TaskID, inputCM)
		in := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name:      inputCM,
				Namespace: m.namespace,
				Labels: map[string]string{
					labelManagedBy: controllerName,
					labelTaskID:    task.TaskID,
				},
			},
			Data: map[string]string{inputFileName: string(task.InputJSON)},
		}
		if _, err := m.client.CoreV1().ConfigMaps(m.namespace).Create(ctx, in, metav1.CreateOptions{}); err != nil {
			m.log.Errorf("task %s: create input configmap failed: %v", task.TaskID, err)
			m.deleteConfigMaps(ctx, configMaps)
			return "", nil, fmt.Errorf("create input configmap: %w", err)
		}
		configMaps = append(configMaps, inputCM)
	}

	job := m.buildJobSpec(cfg, task, jobName, moduleCM, inputCM)
	if _, err := m.client.BatchV1().Jobs(m.namespace).Create(ctx, job, metav1.CreateOptions{}); err != nil {
		m.log.Errorf("task %s: create job %s failed: %v", task.TaskID, jobName, err)
		m.deleteConfigMaps(ctx, configMaps)
		return "", nil, fmt.Errorf("create job: %w", err)
	}

	m.log.Infof("task %s: job %s created successfully", task.TaskID, jobName)
	return jobName, configMaps, nil
}

// WaitForJob 轮询 Job 直到成功、失败或上下文被取消。
func (m *KubeManager) WaitForJob(ctx context.Context, jobName string) (*batchv1.Job, error) {
	m.log.Infof("waiting for job %s to complete", jobName)
	err := wait.PollUntilContextCancel(ctx, 3*time.Second, true, func(ctx context.Context) (bool, error) {
		job, err := m.client.BatchV1().Jobs(m.namespace).Get(ctx, jobName, metav1.GetOptions{})
		if err != nil {
			return false, err
		}
		if job.Status.Failed > 0 || job.Status.Succeeded > 0 {
			return true, nil
		}
		return false, nil
	})
	if err != nil {
		m.log.Warnf("wait job %s interrupted: %v", jobName, err)
		return nil, err
	}
	job, err := m.client.BatchV1().Jobs(m.namespace).Get(ctx, jobName, metav1.GetOptions{})
	if err == nil {
		m.log.Infof("job %s finished (succeeded=%d failed=%d)", jobName, job.Status.Succeeded, job.Status.Failed)
	}
	return job, err
}

// FetchJobLogs 拉取 Job 第一个 Pod 的日志，供协调器解析输出。
func (m *KubeManager) FetchJobLogs(ctx context.Context, jobName string) (string, error) {
	job, err := m.client.BatchV1().Jobs(m.namespace).Get(ctx, jobName, metav1.GetOptions{})
	if err != nil {
		return "", err
	}

	var selector labels.Selector
	if job.Spec.Selector != nil {
		selector = labels.Set(job.Spec.Selector.MatchLabels).AsSelector()
	} else {
		selector = labels.SelectorFromSet(map[string]string{
			labelManagedBy: controllerName,
			labelTaskID:    job.Labels[labelTaskID],
		})
	}
	pods, err := m.client.CoreV1().Pods(m.namespace).List(ctx, metav1.ListOptions{LabelSelector: selector.String()})
	if err != nil {
		return "", err
	}
	if len(pods.Items) == 0 {
		return "", fmt.Errorf("no pod found for job %s", jobName)
	}

	req := m.client.CoreV1().Pods(m.namespace).GetLogs(pods.Items[0].Name, &corev1.PodLogOptions{})
	stream, err := req.Stream(ctx)
	if err != nil {
		return "", err
	}
	defer stream.Close()

	var builder strings.Builder
	scanner := bufio.NewScanner(stream)
	for scanner.Scan() {
		builder.WriteString(scanner.Text())
		builder.WriteByte('\n')
	}
	if err := scanner.Err(); err != nil {
		return "", err
	}
	return builder.String(), nil
}

// DeleteArtifacts 删除 Job 以及本轮创建的 ConfigMap，避免资源残留。
func (m *KubeManager) DeleteArtifacts(ctx context.Context, jobName string, configMaps ...string) {
	m.log.Infof("cleaning up job %s", jobName)
	propagation := metav1.DeletePropagationBackground
	if err := m.client.BatchV1().Jobs(m.namespace).Delete(ctx, jobName, metav1.DeleteOptions{PropagationPolicy: &propagation}); err != nil {
		m.log.Warnf("delete job %s: %v", jobName, err)
	}
	m.deleteConfigMaps(ctx, configMaps)
}

// deleteConfigMaps 逐个删除 ConfigMap（忽略空字符串）。
func (m *KubeManager) deleteConfigMaps(ctx context.Context, configMaps []string) {
	for _, name := range configMaps {
		if name == "" {
			continue
		}
		if err := m.client.CoreV1().ConfigMaps(m.namespace).Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
			m.log.Warnf("delete configmap %s: %v", name, err)
		} else {
			m.log.Infof("configmap %s deleted", name)
		}
	}
}
