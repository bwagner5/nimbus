package applications

import "fmt"

// Step labels for multi-step workflows. The UI should use these so labels
// stay in sync with the actual SDK operations.

// CreateSteps returns the step labels for creating an application.
// If target is non-empty, includes the add-target step and its sub-steps.
func CreateSteps(appName, envName, target string) (labels []string, targetSubs []string) {
	labels = []string{fmt.Sprintf("Create bucket for %s/%s", appName, envName)}
	if target != "" {
		labels = append(labels, fmt.Sprintf("Add target %s", target))
		targetSubs = []string{
			"Tag instance",
			"Create access key",
			"Write credentials to instance",
			"Upload nimbus binary",
			"Start watch service",
		}
	}
	return
}

// DeleteSteps returns the step labels for deleting an application.
func DeleteSteps(appName string) []string {
	return []string{
		"Stop deployments on instances",
		"Remove instance tags",
		"Clean up firewall rules",
		"Delete environment buckets",
	}
}

// DisassociateSteps returns the step labels for disassociating a target.
func DisassociateSteps(instanceName string) (labels []string, cleanupSubs []string) {
	labels = []string{fmt.Sprintf("Disassociate %s", instanceName)}
	cleanupSubs = []string{
		"Stop watch service",
		"Stop running containers",
		"Remove application files",
	}
	return
}

// AddEnvSteps returns the step labels for adding an environment.
func AddEnvSteps(appName, envName string) []string {
	return []string{
		fmt.Sprintf("Create bucket for %s/%s", appName, envName),
		"Update environment order",
	}
}

// PromoteSteps returns the step labels for promoting between environments.
func PromoteSteps(srcEnv, destEnv string) []string {
	return []string{
		fmt.Sprintf("Download latest deploy from %s", srcEnv),
		fmt.Sprintf("Upload to %s", destEnv),
	}
}
