package getversion

import (
	"fmt"
	"os"
	"strings"

	"github.com/run-ai/runai-cli/pkg/client"
	"github.com/spf13/cobra"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func Command() *cobra.Command {
	var command = &cobra.Command{
		Use:   "get-version",
		Short: "get cluster version",
		Run: func(cmd *cobra.Command, args []string) {
			client := client.GetClient()
			deployment, err := client.GetClientset().AppsV1().Deployments("runai").Get("runai-operator", metav1.GetOptions{})
			if err != nil {
				fmt.Println("RunAi is not running on cluster")
				os.Exit(1)
			}
			currentImage := strings.Split(deployment.Spec.Template.Spec.Containers[0].Image, ":")
			fmt.Printf("RunAi version: %v\n", currentImage[1])
		},
	}

	return command
}
