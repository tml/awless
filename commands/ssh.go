package commands

import (
	"fmt"
	"log"
	"strings"

	"golang.org/x/crypto/ssh"

	"github.com/spf13/cobra"
	"github.com/wallix/awless/cloud/aws"
	"github.com/wallix/awless/config"
	"github.com/wallix/awless/console"
	"github.com/wallix/awless/graph"
)

func init() {
	RootCmd.AddCommand(sshCmd)
}

var sshCmd = &cobra.Command{
	Use:                "ssh [user@]instance",
	Short:              "Launch a SSH (Secure Shell) session connecting to an instance",
	PersistentPreRun:   applyHooks(initAwlessEnvHook, initCloudServicesHook, checkStatsHook),
	PersistentPostRunE: saveHistoryHook,

	RunE: func(cmd *cobra.Command, args []string) error {
		if len(args) != 1 {
			return fmt.Errorf("instance required")
		}
		var instanceID string
		var user string
		if strings.Contains(args[0], "@") {
			user = strings.Split(args[0], "@")[0]
			instanceID = strings.Split(args[0], "@")[1]
		} else {
			instanceID = args[0]
		}

		instancesGraph, err := aws.InfraService.FetchByType(graph.Instance.String())
		exitOn(err)

		a := graph.Alias(instanceID)
		if id, ok := a.ResolveToId(instancesGraph, graph.Instance); ok {
			instanceID = id
		}

		cred, err := instanceCredentialsFromGraph(instancesGraph, instanceID)
		exitOn(err)
		var client *ssh.Client
		if user != "" {
			cred.User = user
			client, err = console.NewSSHClient(config.KeysDir, cred)
			exitOn(err)
			if verboseFlag {
				log.Printf("Login as '%s' on '%s', using key '%s'", user, cred.IP, cred.KeyName)
			}
			if err = console.InteractiveTerminal(client); err != nil {
				exitOn(err)
			}
			return nil
		}
		for _, user := range aws.DefaultAMIUsers {
			cred.User = user
			client, err = console.NewSSHClient(config.KeysDir, cred)
			if err != nil && strings.Contains(err.Error(), "unable to authenticate") {
				continue
			}
			exitOn(err)
			log.Printf("Login as '%s' on '%s', using key '%s'", user, cred.IP, cred.KeyName)
			if err = console.InteractiveTerminal(client); err != nil {
				exitOn(err)
			}
			return nil
		}
		return err
	},
}

func instanceCredentialsFromGraph(g *graph.Graph, instanceID string) (*console.Credentials, error) {
	inst, err := g.GetResource(graph.Instance, instanceID)
	if err != nil {
		return nil, err
	}

	ip, ok := inst.Properties["PublicIp"]
	if !ok {
		return nil, fmt.Errorf("no public IP address for instance %s", instanceID)
	}

	key, ok := inst.Properties["KeyName"]
	if !ok {
		return nil, fmt.Errorf("no access key set for instance %s", instanceID)
	}
	return &console.Credentials{IP: fmt.Sprint(ip), User: "", KeyName: fmt.Sprint(key)}, nil
}
