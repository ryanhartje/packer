package shell_local

import (
	"bufio"
	"context"
	"fmt"
	"log"
	"os"
	"runtime"
	"sort"
	"strings"

	"github.com/hashicorp/packer/common"
	commonhelper "github.com/hashicorp/packer/helper/common"
	"github.com/hashicorp/packer/packer"
	"github.com/hashicorp/packer/packer/tmp"
	"github.com/hashicorp/packer/template/interpolate"
)

type ExecuteCommandTemplate struct {
	Vars          string
	Script        string
	Command       string
	WinRMPassword string
}

type EnvVarsTemplate struct {
	WinRMPassword string
}

func Run(ctx context.Context, ui packer.Ui, config *Config) (bool, error) {
	// Check if shell-local can even execute against this runtime OS
	if len(config.OnlyOn) > 0 {
		runCommand := false
		for _, os := range config.OnlyOn {
			if os == runtime.GOOS {
				runCommand = true
				break
			}
		}
		if !runCommand {
			ui.Say(fmt.Sprintf("Skipping shell-local due to runtime OS"))
			log.Printf("[INFO] (shell-local): skipping shell-local due to missing runtime OS")
			return true, nil
		}
	}

	scripts := make([]string, len(config.Scripts))
	if len(config.Scripts) > 0 {
		copy(scripts, config.Scripts)
	} else if config.Inline != nil {
		// If we have an inline script, then turn that into a temporary
		// shell script and use that.
		tempScriptFileName, err := createInlineScriptFile(config)
		if err != nil {
			return false, err
		}

		// figure out what extension the file should have, and rename it.
		if config.TempfileExtension != "" {
			os.Rename(tempScriptFileName, fmt.Sprintf("%s.%s", tempScriptFileName, config.TempfileExtension))
			tempScriptFileName = fmt.Sprintf("%s.%s", tempScriptFileName, config.TempfileExtension)
		}

		scripts = append(scripts, tempScriptFileName)

		defer os.Remove(tempScriptFileName)
	}

	// Create environment variables to set before executing the command
	flattenedEnvVars, err := createFlattenedEnvVars(config)
	if err != nil {
		return false, err
	}

	for _, script := range scripts {
		interpolatedCmds, err := createInterpolatedCommands(config, script, flattenedEnvVars)
		if err != nil {
			return false, err
		}
		ui.Say(fmt.Sprintf("Running local shell script: %s", script))

		comm := &Communicator{
			ExecuteCommand: interpolatedCmds,
		}

		// The remoteCmd generated here isn't actually run, but it allows us to
		// use the same interafce for the shell-local communicator as we use for
		// the other communicators; ultimately, this command is just used for
		// buffers and for reading the final exit status.
		flattenedCmd := strings.Join(interpolatedCmds, " ")
		cmd := &packer.RemoteCmd{Command: flattenedCmd}
		log.Printf("[INFO] (shell-local): starting local command: %s", flattenedCmd)
		if err := cmd.RunWithUi(ctx, comm, ui); err != nil {
			return false, fmt.Errorf(
				"Error executing script: %s\n\n"+
					"Please see output above for more information.",
				script)
		}

		if err := config.ValidExitCode(cmd.ExitStatus()); err != nil {
			return false, err
		}
	}

	return true, nil
}

func createInlineScriptFile(config *Config) (string, error) {
	tf, err := tmp.File("packer-shell")
	if err != nil {
		return "", fmt.Errorf("Error preparing shell script: %s", err)
	}
	defer tf.Close()
	// Write our contents to it
	writer := bufio.NewWriter(tf)
	if config.InlineShebang != "" {
		shebang := fmt.Sprintf("#!%s\n", config.InlineShebang)
		log.Printf("[INFO] (shell-local): Prepending inline script with %s", shebang)
		writer.WriteString(shebang)
	}

	// generate context so you can interpolate the command
	config.ctx.Data = &EnvVarsTemplate{
		WinRMPassword: getWinRMPassword(config.PackerBuildName),
	}

	for _, command := range config.Inline {
		// interpolate command to check for template variables.
		command, err := interpolate.Render(command, &config.ctx)
		if err != nil {
			return "", err
		}

		if _, err := writer.WriteString(command + "\n"); err != nil {
			return "", fmt.Errorf("Error preparing shell script: %s", err)
		}
	}

	if err := writer.Flush(); err != nil {
		return "", fmt.Errorf("Error preparing shell script: %s", err)
	}

	err = os.Chmod(tf.Name(), 0700)
	if err != nil {
		log.Printf("[ERROR] (shell-local): error modifying permissions of temp script file: %s", err.Error())
	}
	return tf.Name(), nil
}

// Generates the final command to send to the communicator, using either the
// user-provided ExecuteCommand or defaulting to something that makes sense for
// the host OS
func createInterpolatedCommands(config *Config, script string, flattenedEnvVars string) ([]string, error) {
	config.ctx.Data = &ExecuteCommandTemplate{
		Vars:          flattenedEnvVars,
		Script:        script,
		Command:       script,
		WinRMPassword: getWinRMPassword(config.PackerBuildName),
	}

	interpolatedCmds := make([]string, len(config.ExecuteCommand))
	for i, cmd := range config.ExecuteCommand {
		interpolatedCmd, err := interpolate.Render(cmd, &config.ctx)
		if err != nil {
			return nil, fmt.Errorf("Error processing command: %s", err)
		}
		interpolatedCmds[i] = interpolatedCmd
	}
	return interpolatedCmds, nil
}

func createFlattenedEnvVars(config *Config) (string, error) {
	flattened := ""
	envVars := make(map[string]string)

	// Always available Packer provided env vars
	envVars["PACKER_BUILD_NAME"] = fmt.Sprintf("%s", config.PackerBuildName)
	envVars["PACKER_BUILDER_TYPE"] = fmt.Sprintf("%s", config.PackerBuilderType)

	// expose ip address variables
	httpAddr := common.GetHTTPAddr()
	if httpAddr != "" {
		envVars["PACKER_HTTP_ADDR"] = httpAddr
	}
	httpIP := common.GetHTTPIP()
	if httpIP != "" {
		envVars["PACKER_HTTP_IP"] = httpIP
	}
	httpPort := common.GetHTTPPort()
	if httpPort != "" {
		envVars["PACKER_HTTP_PORT"] = httpPort
	}

	// interpolate environment variables
	config.ctx.Data = &EnvVarsTemplate{
		WinRMPassword: getWinRMPassword(config.PackerBuildName),
	}
	// Split vars into key/value components
	for _, envVar := range config.Vars {
		envVar, err := interpolate.Render(envVar, &config.ctx)
		if err != nil {
			return "", err
		}
		// Split vars into key/value components
		keyValue := strings.SplitN(envVar, "=", 2)
		// Store pair, replacing any single quotes in value so they parse
		// correctly with required environment variable format
		envVars[keyValue[0]] = strings.Replace(keyValue[1], "'", `'"'"'`, -1)
	}

	// Create a list of env var keys in sorted order
	var keys []string
	for k := range envVars {
		keys = append(keys, k)
	}
	sort.Strings(keys)

	for _, key := range keys {
		flattened += fmt.Sprintf(config.EnvVarFormat, key, envVars[key])
	}
	return flattened, nil
}

func getWinRMPassword(buildName string) string {
	winRMPass, _ := commonhelper.RetrieveSharedState("winrm_password", buildName)
	packer.LogSecretFilter.Set(winRMPass)
	return winRMPass
}
