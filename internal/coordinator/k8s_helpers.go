package coordinator

import (
	"fmt"
	"regexp"
	"strings"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
)

// 常量统一了标签、卷名与挂载路径，确保模板与运行时代码一致。
const (
	labelManagedBy   = "executor.wasm/managing-controller"
	labelTaskID      = "executor.wasm/task-id"
	labelConfigMap   = "executor.wasm/config-map"
	labelJobTemplate = "executor.wasm/template"
	controllerName   = "wasm-coordinator"

	wasmFileName    = "module.wasm"
	wasmMountPath   = "/mnt/wasm"
	sharedMountPath = "/mnt/shared"
	resultFileName  = "result.json"
	inputFileName   = "input.json"
	inputMountPath  = "/mnt/input"
	inputVolumeName = "input-dir"
	wasmVolumeName  = "wasm-dir"
)

// nameSanitizer 将任务 ID 清洗成合法的 Kubernetes 名称。
var nameSanitizer = regexp.MustCompile(`[^a-z0-9\-]+`)

// sanitizeName 统一裁剪/小写 Task ID，避免非法或超长名称。
func sanitizeName(base string) string {
	base = strings.ToLower(base)
	base = nameSanitizer.ReplaceAllString(base, "-")
	base = strings.Trim(base, "-")
	if len(base) == 0 {
		base = "task"
	}
	if len(base) > 50 {
		base = base[:50]
	}
	return base
}

func (m *KubeManager) configMapName(taskID string) string {
	return fmt.Sprintf("wasm-task-%s", sanitizeName(taskID))
}

func (m *KubeManager) inputConfigMapName(taskID string) string {
	return fmt.Sprintf("wasm-input-%s", sanitizeName(taskID))
}

func (m *KubeManager) jobName(taskID string) string {
	return fmt.Sprintf("wasm-job-%s", sanitizeName(taskID))
}

// buildJobSpec 根据模板注入任务专属 env、标签与 ConfigMap 卷。
func (m *KubeManager) buildJobSpec(cfg Config, task TaskRequest, jobName, wasmCMName, inputCMName string) *batchv1.Job {
	tmpl := m.template.DeepCopy()

	tmpl.Namespace = cfg.Namespace
	tmpl.Name = jobName
	tmpl.Labels = mergeLabels(tmpl.Labels, map[string]string{
		labelManagedBy:   controllerName,
		labelTaskID:      task.TaskID,
		labelConfigMap:   wasmCMName,
		labelJobTemplate: "executor-v1",
	})

	podMeta := &tmpl.Spec.Template.ObjectMeta
	podMeta.Labels = mergeLabels(podMeta.Labels, map[string]string{
		labelManagedBy: controllerName,
		labelTaskID:    task.TaskID,
	})

	appendEnv := func(envs []corev1.EnvVar, name, value string) []corev1.EnvVar {
		if value == "" {
			return envs
		}
		for i := range envs {
			if envs[i].Name == name {
				envs[i].Value = value
				return envs
			}
		}
		return append(envs, corev1.EnvVar{Name: name, Value: value})
	}

	inputPath := fmt.Sprintf("%s/%s", sharedMountPath, inputFileName)
	if inputCMName != "" {
		inputPath = fmt.Sprintf("%s/%s", inputMountPath, inputFileName)
	}

	env := []corev1.EnvVar{}
	env = appendEnv(env, "WASM_PATH", fmt.Sprintf("%s/%s", wasmMountPath, wasmFileName))
	env = appendEnv(env, "OUTPUT_PATH", fmt.Sprintf("%s/%s", sharedMountPath, resultFileName))
	env = appendEnv(env, "INPUT_PATH", inputPath)
	if task.Entry != "" {
		env = appendEnv(env, "ENTRY", task.Entry)
	}
	for k, v := range tn(task.Args) {
		env = appendEnv(env, k, v)
	}

	for i := range tmpl.Spec.Template.Spec.Containers {
		c := &tmpl.Spec.Template.Spec.Containers[i]
		if cfg.ExecutorImage != "" {
			c.Image = cfg.ExecutorImage
		}
		c.Env = env
		if inputCMName != "" {
			ensureVolumeMount(c, inputVolumeName, inputMountPath, true)
		}
	}

	vols := &tmpl.Spec.Template.Spec.Volumes
	ensureConfigMapVolume(vols, wasmVolumeName, wasmCMName)
	if inputCMName != "" {
		ensureConfigMapVolume(vols, inputVolumeName, inputCMName)
	}

	return tmpl
}

// ensureConfigMapVolume 确保 Pod 规格中存在指向 cmName 的 ConfigMap 卷。
func ensureConfigMapVolume(vols *[]corev1.Volume, name, cmName string) {
	src := corev1.VolumeSource{
		ConfigMap: &corev1.ConfigMapVolumeSource{
			LocalObjectReference: corev1.LocalObjectReference{Name: cmName},
		},
	}
	for i := range *vols {
		if (*vols)[i].Name == name {
			(*vols)[i].VolumeSource = src
			return
		}
	}
	*vols = append(*vols, corev1.Volume{Name: name, VolumeSource: src})
}

// ensureVolumeMount 确保容器挂载指定卷并更新挂载属性。
func ensureVolumeMount(c *corev1.Container, name, mountPath string, readOnly bool) {
	for i := range c.VolumeMounts {
		if c.VolumeMounts[i].Name == name {
			c.VolumeMounts[i].MountPath = mountPath
			c.VolumeMounts[i].ReadOnly = readOnly
			return
		}
	}
	c.VolumeMounts = append(c.VolumeMounts, corev1.VolumeMount{
		Name:      name,
		MountPath: mountPath,
		ReadOnly:  readOnly,
	})
}

// tn 将 nil map 替换为可遍历的空 map，方便统一构造 env。
func tn(in map[string]string) map[string]string {
	if in == nil {
		return map[string]string{}
	}
	return in
}

// mergeLabels 以覆盖方式合并标签，src 优先。
func mergeLabels(dst map[string]string, src map[string]string) map[string]string {
	if dst == nil {
		dst = map[string]string{}
	}
	for k, v := range src {
		dst[k] = v
	}
	return dst
}
