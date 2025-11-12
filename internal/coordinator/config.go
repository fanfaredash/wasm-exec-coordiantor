package coordinator

// Config 描述协调器运行所需的最小配置信息。
type Config struct {
	Namespace     string
	ExecutorImage string
	JobTemplate   string
	Log           Logger
}

// applyDefaults 为缺失的配置填充默认值。
func (c *Config) applyDefaults() {
	if c.Namespace == "" {
		c.Namespace = "default"
	}
	if c.ExecutorImage == "" {
		c.ExecutorImage = "executor-demo/executor:demo"
	}
	if c.JobTemplate == "" {
		c.JobTemplate = "k8s/job.yaml"
	}
}
