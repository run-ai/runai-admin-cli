package nodes

import (
	"fmt"
	"strings"

	"github.com/run-ai/runai-cli/cmd/trainer"
	"github.com/run-ai/runai-cli/pkg/helpers"
	"github.com/run-ai/runai-cli/pkg/types"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"

	log "github.com/sirupsen/logrus"

	"github.com/run-ai/runai-cli/cmd/util"
	prom "github.com/run-ai/runai-cli/pkg/prometheus"

	v1 "k8s.io/api/core/v1"
)

const (

	// prometheus query names
	TotalGpusMemoryPQ = "totalGpusMemory"
	UsedGpusMemoryPQ  = "usedGpusMemory"
	UsedCpusMemoryPQ  = "usedCpusMemory"
	UsedCpusPQ        = "usedCpus"
	UsedGpusPQ        = "usedGpus"
	GpuIdleTimePQ     = "gpuIdleTime"
	UsedGpuPQ         = "usedGpu"
	GpuUsedByPod      = "gpuUsedByPod"
	UsedGpuMemoryPQ   = "usedGpuMemory"
	TotalGpuMemoryPQ  = "totalGpuMemory"
)

var (
	promethesNodeLabelID = "node"
	nodePQs              = prom.QueryNameToQuery{
		TotalGpusMemoryPQ: `(sum(runai_node_gpu_total_memory * 1024 * 1024) by (node))`,
		UsedGpusPQ:        `((sum(runai_node_gpu_utilization) by (node)) / on (node) (count(runai_node_gpu_utilization) by (node)))`,
		UsedGpusMemoryPQ:  `(sum(runai_node_gpu_used_memory * 1024 * 1024) by (node))`,
		UsedCpusMemoryPQ:  `runai_node_memory_used_bytes`,
		UsedCpusPQ:        `runai_node_cpu_utilization * 100`,
		UsedGpuPQ:         `(sum(runai_node_gpu_utilization) by (node, gpu))`,
		UsedGpuMemoryPQ:   `(sum(runai_node_gpu_used_memory * 1024 * 1024) by (node, gpu))`,
		TotalGpuMemoryPQ:  `(sum(runai_node_gpu_total_memory * 1024 * 1024) by (node, gpu))`,
		GpuIdleTimePQ:     `(sum(time()-runai_node_gpu_last_not_idle_time) by (node, gpu))`,
		GpuUsedByPod:      `sum(runai_gpus_is_running_with_pod2 * 100) by (node, gpu)`,
	}
)

type NodeInfo struct {
	Node           v1.Node
	Pods           []v1.Pod
	PrometheusData prom.MetricResultsByQueryName
}

func (ni *NodeInfo) GetStatus() types.NodeStatus {
	if !util.IsNodeReady(ni.Node) {
		return types.NodeNotReady
	}
	return types.NodeReady
}

func (ni *NodeInfo) GetGeneralInfo() types.NodeGeneralInfo {
	return types.NodeGeneralInfo{
		Name:      ni.Node.Name,
		Role:      strings.Join(util.GetNodeRoles(&ni.Node), ","),
		IPAddress: util.GetNodeInternalAddress(ni.Node),
		Status:    ni.GetStatus(),
	}
}

func (ni *NodeInfo) GetResourcesStatus() types.NodeResourcesStatus {

	nodeResStatus := types.NodeResourcesStatus{}
	podResStatus := types.PodResourcesStatus{}

	for _, pod := range ni.Pods {
		helpers.AddToPodResourcesStatus(&podResStatus, helpers.GetPodResourceStatus(pod))
	}

	// adding the kube data
	nodeResStatus.Requested = podResStatus.Requested
	nodeResStatus.Allocated = podResStatus.Requested
	nodeResStatus.Allocated.GPUs = podResStatus.Allocated.GPUs // needed to count fractions as well
	nodeResStatus.Limited = podResStatus.Limited

	helpers.AddKubeResourceListToResourceList(&nodeResStatus.Capacity, ni.Node.Status.Capacity)
	// fix the gpus capacity (when there is a job that using fractional gpu the gpu will not appear in the node > status > capacity so we need to override the capacity.gpus  )
	totalGpus := int(util.AllocatableGpuInNodeIncludingFractions(ni.Node))
	// check that the totalGpus is set
	isFractionRunningOnNode := totalGpus > int(nodeResStatus.Capacity.GPUs)
	if isFractionRunningOnNode {
		nodeResStatus.NumberOfFractionalAllocatedGpu = len(util.GetSharedGPUsIndexUsedInPods(ni.Pods))
		nodeResStatus.Capacity.GPUs = float64(totalGpus)
		// update the allocatable too
		nodeResStatus.Allocatable.GPUs += float64(nodeResStatus.NumberOfFractionalAllocatedGpu)
	}

	helpers.AddKubeResourceListToResourceList(&nodeResStatus.Allocatable, ni.Node.Status.Allocatable)
	nodeResStatus.GPUsInUse = nodeResStatus.NumberOfFractionalAllocatedGpu + int(podResStatus.Limited.GPUs)

	// adding the prometheus data

	if ni.PrometheusData != nil {
		// set usages
		err := hasError(
			prom.SetFloatFromFirstMetric(&nodeResStatus.Usage.CPUs, ni.PrometheusData, UsedCpusPQ),
			prom.SetFloatFromFirstMetric(&nodeResStatus.Usage.GPUs, ni.PrometheusData, UsedGpusPQ),
			prom.SetFloatFromFirstMetric(&nodeResStatus.Usage.Memory, ni.PrometheusData, UsedCpusMemoryPQ),
			prom.SetFloatFromFirstMetric(&nodeResStatus.Usage.GPUMemory, ni.PrometheusData, UsedGpusMemoryPQ),
			// setFloatPromData(&nodeResStatus.Usage.Storage, p, UsedStoragePQ)

			// set total
			prom.SetFloatFromFirstMetric(&nodeResStatus.Capacity.GPUMemory, ni.PrometheusData, TotalGpusMemoryPQ),
			setGpusFromPromDataAndPods(&nodeResStatus.NodeGPUs, ni.PrometheusData, ni.Pods),
		)

		if err != nil {
			log.Debugf("Failed to extract prometheus data, %v", err)
		}
	}

	return nodeResStatus
}

func (nodeInfo *NodeInfo) IsGPUExclusiveNode() bool {
	value, ok := nodeInfo.Node.Status.Allocatable[util.NVIDIAGPUResourceName]

	if ok {
		ok = value.Value() > 0
	}

	return ok
}

func setGpusFromPromDataAndPods(value *[]types.GPU, data prom.MetricResultsByQueryName, pods []v1.Pod) error {
	result := []types.GPU{}
	metricsValuesByGpus, err := prom.GroupMetrics("gpu", data, GpuIdleTimePQ, UsedGpuPQ, UsedGpuMemoryPQ, TotalGpuMemoryPQ, GpuUsedByPod)

	if err != nil {
		return err
	}

	fractionAllocatedGpus := util.GetSharedGPUsIndexUsedInPods(pods)

	for gpuIndex, valuesByQueryNames := range metricsValuesByGpus {

		allocated := valuesByQueryNames[GpuUsedByPod]
		fractionAllocated, isFraction := fractionAllocatedGpus[gpuIndex]
		if isFraction {
			allocated = fractionAllocated
		}

		memory := valuesByQueryNames[TotalGpuMemoryPQ]
		usage := valuesByQueryNames[UsedGpuMemoryPQ]
		memoryUsageAndUtilization, utilization := helpers.MemoryUsageAndUtilization(usage, memory)

		result = append(result, types.GPU{
			IndexID:                   gpuIndex,
			Allocated:                 allocated,
			Memory:                    memory,
			MemoryUsage:               usage,
			MemoryUtilization:         utilization,
			MemoryUsageAndUtilization: memoryUsageAndUtilization,
			IdleTime:                  valuesByQueryNames[GpuIdleTimePQ],
			Utilization:               valuesByQueryNames[UsedGpuPQ],
		})
	}

	*value = result
	return nil
}

func hasError(errors ...error) error {
	for _, err := range errors {
		if err != nil {
			return err
		}
	}
	return nil
}

func GetAllNodeInfos(client kubernetes.Interface, shouldQueryMetrics bool) ([]NodeInfo, string, error) {
	var warning string
	nodeInfoList := []NodeInfo{}
	allActivePods, err := trainer.AcquireAllActivePods(client)
	if err != nil {
		return nil, "", err
	}

	nodeList, err := client.CoreV1().Nodes().List(metav1.ListOptions{})

	if err != nil {
		return nodeInfoList, warning, err
	}

	var promData prom.MetricResultsByItems
	if shouldQueryMetrics {
		promClient, promErr := prom.BuildPrometheusClient(client)
		if promErr == nil {
			promData, promErr = promClient.GroupMultiQueriesToItems(nodePQs, promethesNodeLabelID)
		}
		if promErr != nil {
			warning = fmt.Sprintf("Missing some data. \nreason: Can't access to the prometheus server, \ncause error: %s", promErr)
		}
	}

	for _, node := range nodeList.Items {
		pods := getPodsOnSpecificNode(node, allActivePods)

		nodeInfo := NodeInfo{
			Node: node,
			Pods: pods,
		}
		if promData != nil {
			nodeInfo.PrometheusData = promData[nodeInfo.Node.Name]
		}
		nodeInfoList = append(nodeInfoList, nodeInfo)
	}

	return nodeInfoList, warning, err
}

func getPodsOnSpecificNode(node v1.Node, allActivePods []v1.Pod) []v1.Pod {
	pods := []v1.Pod{}
	if !util.IsNodeReady(node) {
		return pods
	}
	for _, pod := range allActivePods {
		if pod.Spec.NodeName == node.Name &&
			(pod.Status.Phase == v1.PodRunning || pod.Status.Phase == v1.PodPending) {
			pods = append(pods, pod)
		}
	}

	return pods
}