package flags

import (
	"fmt"
	"strconv"

	constants "github.com/kubeflow/arena/cmd/arena/commands/constants"
	"github.com/kubeflow/arena/cmd/arena/commands/util"
	"github.com/kubeflow/arena/cmd/arena/types"
	"github.com/kubeflow/arena/pkg/client"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// This function will print an error even if -b flag was used
func GetNamespaceToUseFromProjectFlagAndPrintError(cmd *cobra.Command, kubeClient *client.Client) (types.NamespaceInfo, error) {
	return getNamespaceToUseFromProjectFlag(cmd, kubeClient, true)
}

// This function will print an error if necessary (no project exists and the user did not use -b flag)
func GetNamespaceToUseFromProjectFlag(cmd *cobra.Command, kubeClient *client.Client) (types.NamespaceInfo, error) {
	return getNamespaceToUseFromProjectFlag(cmd, kubeClient, false)
}

// Return the namespace to use on first argument. on second argument get
func getNamespaceToUseFromProjectFlag(cmd *cobra.Command, kubeClient *client.Client, ignoreBackwardFlagOnError bool) (types.NamespaceInfo, error) {
	namespaceInfo, err := getNamespaceInfoToUseFromProjectFlag(cmd, kubeClient)

	if err != nil {
		return namespaceInfo, err
	}

	if shouldPrintSetDefaultMessage(namespaceInfo, ignoreBackwardFlagOnError) {
		fmt.Println("Please set a default project by running ‘runai project set <project-name>’ or use the flag -p to use a specific project.")
	}

	return namespaceInfo, nil
}

func getNamespaceInfoToUseFromProjectFlag(cmd *cobra.Command, kubeClient *client.Client) (types.NamespaceInfo, error) {
	backwardCompitabilityString := getFlagValue(cmd, BackwardCompitableFlag)
	backwardCompitability, err := strconv.ParseBool(backwardCompitabilityString)
	if err != nil {
		return types.NamespaceInfo{}, err
	}

	if backwardCompitability {
		return types.NamespaceInfo{
			Namespace:             "default",
			BackwardCompatability: true,
		}, nil
	}

	flagValue := getFlagValue(cmd, ProjectFlag)
	if flagValue != "" {
		namespace, err := util.GetNamespaceFromProjectName(flagValue, kubeClient)
		return types.NamespaceInfo{
			Namespace:   namespace,
			ProjectName: flagValue,
		}, err
	}

	namespace := kubeClient.GetDefaultNamespace()
	projectName, err := getProjectRelatedToNamespace(namespace, kubeClient)

	if err != nil {
		return types.NamespaceInfo{
			Namespace:   "",
			ProjectName: "",
		}, err
	}

	return types.NamespaceInfo{
		Namespace:   namespace,
		ProjectName: projectName,
	}, nil
}

func getProjectRelatedToNamespace(namespaceName string, kubeClient *client.Client) (string, error) {
	namespace, err := kubeClient.GetClientset().CoreV1().Namespaces().Get(namespaceName, metav1.GetOptions{})

	if err != nil {
		return "", err
	}

	if namespace.Labels == nil {
		return "", nil
	}

	return namespace.Labels[constants.RUNAI_QUEUE_LABEL], nil
}

func shouldPrintSetDefaultMessage(namespaceInfo types.NamespaceInfo, ignoreBackwardFlagOnError bool) bool {
	return namespaceInfo.ProjectName == "" && (ignoreBackwardFlagOnError || !namespaceInfo.BackwardCompatability)
}

func GetNamespaceToUseFromProjectFlagIncludingAll(cmd *cobra.Command, kubeClient *client.Client, allFlag bool) (types.NamespaceInfo, error) {
	if allFlag {
		return types.NamespaceInfo{
			Namespace:   metav1.NamespaceAll,
			ProjectName: types.ALL_PROJECTS,
		}, nil
	} else {
		namespaceInfo, err := getNamespaceInfoToUseFromProjectFlag(cmd, kubeClient)

		if err != nil {
			return namespaceInfo, err
		}

		if shouldPrintSetDefaultMessage(namespaceInfo, false) {
			fmt.Println("Please set a default project by running ‘runai project set <project-name>’ or use the flag -A to view all projects, or use the flag -p to view a specific project.")
		}

		return namespaceInfo, nil
	}
}

func getFlagValue(cmd *cobra.Command, name string) string {
	return cmd.Flags().Lookup(name).Value.String()
}