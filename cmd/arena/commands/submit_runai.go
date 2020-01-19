package commands

import (
	"fmt"
	"github.com/kubeflow/arena/pkg/config"
	"github.com/kubeflow/arena/pkg/util"
	"github.com/kubeflow/arena/pkg/util/kubectl"
	"github.com/kubeflow/arena/pkg/workflow"
	log "github.com/sirupsen/logrus"
	"github.com/spf13/cobra"
	v1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"math"
	"os"
	"os/user"
	"path"
	"regexp"
	"strconv"
	"strings"
	"time"
)

var (
	runaiChart              = path.Join(util.GetChartsFolder(), "runai")
	nameArg                 string
	noIndex                 bool
	ttlSecondsAfterFinished string
)

const (
	defaultRunaiTrainingType = "runai"
)

func NewRunaiJobCommand() *cobra.Command {
	submitArgs := NewSubmitRunaiJobArgs()
	var command = &cobra.Command{
		Use:     "submit",
		Short:   "Submit a Runai job.",
		Aliases: []string{"ra"},
		Run: func(cmd *cobra.Command, args []string) {

			util.SetLogLevel(logLevel)
			setupKubeconfig()

			_, err := initKubeClient()
			if err != nil {
				fmt.Println(err)
				os.Exit(1)
			}

			err = updateNamespace(cmd)
			if err != nil {
				log.Debugf("Failed due to %v", err)
				fmt.Println(err)
				os.Exit(1)
			}

			if noIndex {
				name = nameArg
			} else {
				index, err := getJobIndex()
				if err != nil {
					log.Error("Could not get index for new job")
					os.Exit(1)
				}

				name = fmt.Sprintf("%s-%s", nameArg, index)
			}

			if ttlSecondsAfterFinished != "" {
				ttl, err := time.ParseDuration(ttlSecondsAfterFinished)
				if err != nil {
					fmt.Println("Wrong format for duration argument")
					os.Exit(1)
				}

				ttlSeconds := int(math.Round(ttl.Seconds()))
				log.Debugf("Using time to live seconds %d", ttlSeconds)
				submitArgs.TTL = &ttlSeconds
			}

			if submitArgs.IsJupyter {
				submitArgs.UseJupyterDefaultValues()
			}

			if submitArgs.ServiceType != "" && len(submitArgs.Ports) == 0 {
				log.Error("Ports must be specified when specifying a service type.")
				os.Exit(1)
			}

			err = submitRunaiJob(args, submitArgs)
			if err != nil {
				fmt.Println(err)
				os.Exit(1)
			}

			if submitArgs.IsJupyter || (submitArgs.Interactive && submitArgs.ServiceType == "portforward") {
				err = kubectl.WaitForReadyStatefulSet(name, namespace)

				if err != nil {
					fmt.Println(err)
					os.Exit(1)
				}

				if submitArgs.IsJupyter {
					runaiTrainer := NewRunaiTrainer(clientset)
					job, err := runaiTrainer.GetTrainingJob(name, namespace)

					if err != nil {
						fmt.Println(err)
						os.Exit(1)
					}

					pod := job.ChiefPod()
					logs, err := kubectl.Logs(pod.Name, pod.Namespace)

					token, err := getTokenFromJupyterLogs(string(logs))

					if err != nil {
						fmt.Println(err)
						fmt.Printf("Please run '%s logs %s' to view the logs.\n", config.CLIName, name)
					}

					fmt.Printf("Jupyter notebook token: %s\n", token)
				}

				if submitArgs.Interactive && submitArgs.ServiceType == "portforward" {
					localPorts := []string{}
					for _, port := range submitArgs.Ports {
						split := strings.Split(port, ":")
						localPorts = append(localPorts, split[0])
					}

					localUrls := []string{}
					for _, localPort := range localPorts {
						localUrls = append(localUrls, fmt.Sprintf("localhost:%s", localPort))
					}

					accessPoints := strings.Join(localUrls, ",")
					fmt.Printf("Open access point(s) to service from %s\n", accessPoints)
					err = kubectl.PortForward(localPorts, name, namespace)
					if err != nil {
						fmt.Println(err)
						os.Exit(1)
					}
				}
			}
		},
	}

	submitArgs.addFlags(command)

	return command
}

func getJobIndex() (string, error) {
	for true {
		index, shouldTryAgain, err := tryGetJobIndexOnce()

		if index != "" || !shouldTryAgain {
			return index, err
		}
	}

	return "", nil
}

func tryGetJobIndexOnce() (string, bool, error) {
	var (
		indexKey       = "index"
		runaiNamespace = "runai"
		configMapName  = "runai-cli-index"
	)

	configMap, err := clientset.CoreV1().ConfigMaps(runaiNamespace).Get(configMapName, metav1.GetOptions{})

	// If configmap does not exists try to create it
	if err != nil {
		data := make(map[string]string)
		data[indexKey] = "1"
		configMap := &v1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{
				Name: configMapName,
			},
			Data: data,
		}

		_, err := clientset.CoreV1().ConfigMaps(runaiNamespace).Create(configMap)

		// Might be someone already created this configmap. Try the process again.
		if err != nil {
			return "", true, nil
		}

		return "1", false, nil
	}

	lastIndex, err := strconv.Atoi(configMap.Data[indexKey])

	if err != nil {
		return "", false, err
	}

	newIndex := fmt.Sprintf("%d", lastIndex+1)
	configMap.Data[indexKey] = newIndex
	_, err = clientset.CoreV1().ConfigMaps(runaiNamespace).Update(configMap)

	// Might be someone already updated this configmap. Try the process again.
	if err != nil {
		return "", true, err
	}

	return newIndex, false, nil
}

func getTokenFromJupyterLogs(logs string) (string, error) {
	re, err := regexp.Compile(`\?token=(.*)\n`)
	if err != nil {
		return "", err
	}

	res := re.FindStringSubmatch(logs)
	if len(res) < 2 {
		return "", fmt.Errorf("Could not find token string in logs")
	}
	return res[1], nil
}

func NewSubmitRunaiJobArgs() *submitRunaiJobArgs {
	return &submitRunaiJobArgs{}
}

type submitRunaiJobArgs struct {
	Project             string   `yaml:"project"`
	GPU                 int      `yaml:"gpu"`
	Image               string   `yaml:"image"`
	HostIPC             bool     `yaml:"hostIPC"`
	Interactive         bool     `yaml:"interactive"`
	Volumes             []string `yaml:"volumes"`
	NodeType            string   `yaml:"node_type"`
	User                string   `yaml:"user"`
	Ports               []string `yaml:"ports"`
	ServiceType         string   `yaml:"serviceType"`
	Command             []string `yaml:"command"`
	Args                []string `yaml:"args"`
	IsJupyter           bool
	CPU                 string   `yaml:"cpu"`
	Memory              string   `yaml:"memory"`
	Elastic             bool     `yaml:"elastic"`
	LargeShm            bool     `yaml:"shm"`
	EnvironmentVariable []string `yaml:"environment"`
	LocalImage          bool     `yaml:"localImage"`
	TTL                 *int     `yaml:"ttlSecondsAfterFinished"`
}

func (sa *submitRunaiJobArgs) UseJupyterDefaultValues() {
	var (
		jupyterPort        = "8888"
		jupyterImage       = "jupyter/scipy-notebook"
		jupyterCommand     = "start-notebook.sh"
		jupyterArgs        = "--NotebookApp.base_url=/%s"
		jupyterServiceType = "portforward"
	)

	sa.Interactive = true
	if len(sa.Ports) == 0 {
		sa.Ports = []string{jupyterPort}
		log.Infof("Exposing default jupyter notebook port %s", jupyterPort)
	}
	if sa.Image == "" {
		sa.Image = "jupyter/scipy-notebook"
		log.Infof("Using default jupyter notebook image \"%s\"", jupyterImage)
	}
	if sa.ServiceType == "" {
		sa.ServiceType = jupyterServiceType
		log.Infof("Using default jupyter notebook service type %s", jupyterServiceType)
	}
	if len(sa.Command) == 0 && sa.ServiceType == "ingress" {
		sa.Command = []string{jupyterCommand}
		log.Infof("Using default jupyter notebook command for using ingress service \"%s\"", jupyterCommand)
	}
	if len(sa.Args) == 0 && sa.ServiceType == "ingress" {
		baseUrlArg := fmt.Sprintf(jupyterArgs, name)
		sa.Args = []string{baseUrlArg}
		log.Infof("Using default jupyter notebook command argument for using ingress service \"%s\"", baseUrlArg)
	}
}

// add flags to submit spark args
func (sa *submitRunaiJobArgs) addFlags(command *cobra.Command) {
	var defaultUser string
	currentUser, err := user.Current()
	if err != nil {
		defaultUser = ""
	} else {
		defaultUser = currentUser.Username
	}

	command.Flags().StringVar(&nameArg, "name", "", "Job name")
	command.MarkFlagRequired("name")

	command.Flags().IntVarP(&(sa.GPU), "gpu", "g", 0, "Number of GPUs to allocation to the Job.")
	command.Flags().StringVar(&(sa.CPU), "cpu", "", "CPU units to allocate for the job (0.5, 1, .etc)")
	command.Flags().StringVar(&(sa.Memory), "memory", "", "Memory to allocate for this job (1G, 20M, .etc)")
	command.Flags().StringVarP(&(sa.Project), "project", "p", "default", "Specifies the Run:AI project to use for this Job.")
	command.Flags().StringVarP(&(sa.Image), "image", "i", "", "Image to use when creating the container for this Job.")
	command.Flags().BoolVar(&(sa.HostIPC), "host-ipc", false, "Use the host's ipc namespace. (default 'false').")
	command.Flags().BoolVar(&(sa.Interactive), "interactive", false, "Mark this Job as unattended or interactive. (default 'false')")
	command.Flags().StringArrayVarP(&(sa.Volumes), "volumes", "v", []string{}, "Volumes to mount into the container.")
	command.Flags().StringVar(&(sa.NodeType), "node-type", "", "Enforce node type affinity by setting a node-type label.")
	command.Flags().StringVarP(&(sa.User), "user", "u", defaultUser, "Use different user to run the Job.")
	command.Flags().StringArrayVar(&(sa.Ports), "port", []string{}, "Expose ports from the Job container.")
	command.Flags().StringVarP(&(sa.ServiceType), "service-type", "s", "", "Service exposure method for interactive Job. Options are: portforward, loadbalancer, nodeport, ingress.")
	command.Flags().StringArrayVar(&(sa.Command), "command", []string{}, "Run this command on container start. Use together with --args.")
	command.Flags().StringArrayVar(&(sa.Args), "args", []string{}, "Arguments to pass to the command run on container start. Use together with --command.")
	command.Flags().BoolVar(&(sa.IsJupyter), "jupyter", false, "Shortcut for running a jupyter notebook container. Uses a pre-created image and a default notebook configuration.")
	command.Flags().BoolVar(&(sa.Elastic), "elastic", false, "Mark the job as elastic.")
	command.Flags().BoolVar(&(sa.LargeShm), "large-shm", false, "Mount a large /dev/shm device. Specific software might need this feature.")
	command.Flags().BoolVar(&(sa.LocalImage), "local-image", false, "Use a local image for this job. NOTE: this image must exists on the local server.")
	command.Flags().BoolVar(&(noIndex), "no-index", false, "Do not add index number to created job.")
	command.Flags().StringArrayVarP(&(sa.EnvironmentVariable), "environment", "e", []string{}, "Define environment variable to be set in the container.")

	command.Flags().StringVar(&(ttlSecondsAfterFinished), "ttl-after-finish", "", "Define the duration, post job finish, after which the job is automatically deleted (5s, 2m, or 3h, .etc).")

	command.Flags().MarkHidden("user")
}

func submitRunaiJob(args []string, submitArgs *submitRunaiJobArgs) error {

	err := workflow.SubmitJob(name, defaultRunaiTrainingType, namespace, submitArgs, runaiChart, clientset)
	if err != nil {
		return err
	}

	log.Infof("The Job %s has been submitted successfully", name)
	log.Infof("You can run `%s get %s` to check the job status", config.CLIName, name)
	return nil
}
