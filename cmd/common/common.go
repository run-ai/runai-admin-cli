package common

import (
	"os"

	"github.com/run-ai/runai-cli/pkg/client"
	log "github.com/sirupsen/logrus"
	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	NumberOfRetiresForApiServer        = 3
	RunaiNamespace                     = "runai"
	RunaiBackendNamespace              = "runai-backend"
	RunaiOperatorDeploymentName        = "runai-operator"
	RunaiBackendOperatorDeploymentName = "helm-operator"
)

func ScaleRunaiOperator(client *client.Client, replicas int32) {
	scaleDeployment(client, RunaiNamespace, RunaiOperatorDeploymentName, replicas)
}

func ScaleRunaiBackendOperator(client *client.Client, replicas int32) {
	scaleDeployment(client, RunaiBackendNamespace, RunaiBackendOperatorDeploymentName, replicas)
}

func scaleDeployment(client *client.Client, namespace, deploymentName string, replicas int32) {
	var err error
	var deployment *appsv1.Deployment
	for i := 0; i < NumberOfRetiresForApiServer; i++ {
		deployment, err = client.GetClientset().AppsV1().Deployments(namespace).Get(deploymentName, metav1.GetOptions{})
		if err != nil {
			log.Infof("Failed to get %s, error: %v", deploymentName, err)
			os.Exit(1)
		}
		deployment.Spec.Replicas = &replicas
		deployment, err = client.GetClientset().AppsV1().Deployments(namespace).Update(deployment)
		if err != nil {
			log.Debugf("Failed to update %s, attempt: %v, error: %v", deploymentName, i, err)
			continue
		}
		break
	}
	if err != nil {
		log.Infof("Failed to update %s, error: %v", deploymentName, err)
		os.Exit(1)
	}
	log.Infof("Scaled %s to: %v", deploymentName, replicas)
}
