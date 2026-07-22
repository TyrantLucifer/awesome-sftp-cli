package app

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"

	"github.com/TyrantLucifer/awesome-sftp-cli/internal/domain"
	"github.com/TyrantLucifer/awesome-sftp-cli/internal/platform"
)

type installPreflightOptions struct {
	prefix      string
	managedRoot bool
	format      string
}

type installPreflightOutput struct {
	OutputVersion int  `json:"output_version"`
	Safe          bool `json:"safe"`
}

func runInstallCommand(_ context.Context, args []string, stdout io.Writer, _ io.Writer) error {
	return runInstallCommandWithPreflight(args, stdout, platform.PreflightInstallation)
}

func runInstallCommandWithPreflight(args []string, stdout io.Writer, preflight func(string, bool) error) error {
	options, err := parseInstallPreflight(args)
	if err != nil {
		return machineCommandError(args, NewExitError(ExitUsage, err))
	}
	if err := preflight(options.prefix, options.managedRoot); err != nil {
		opErr := &domain.OpError{
			Code:      domain.CodeIntegrityFailed,
			Operation: "preflight install",
			Message:   "installation paths do not satisfy the local trust policy",
			Cause:     err,
		}
		return machineCommandError(args, NewExitError(ExitConfig, opErr))
	}
	if options.format == "json" {
		return json.NewEncoder(stdout).Encode(installPreflightOutput{OutputVersion: PublicCLIContractVersion, Safe: true})
	}
	_, err = fmt.Fprintln(stdout, "AMSFTP installation paths are trusted")
	return err
}

func parseInstallPreflight(args []string) (installPreflightOptions, error) {
	if len(args) == 0 || args[0] != "preflight" {
		return installPreflightOptions{}, errors.New("internal install command requires preflight")
	}
	options := installPreflightOptions{format: "human"}
	formatSet := false
	for index := 1; index < len(args); index++ {
		switch args[index] {
		case "--prefix", "--root":
			if index+1 >= len(args) || options.prefix != "" {
				return installPreflightOptions{}, errors.New("preflight requires exactly one install path")
			}
			options.managedRoot = args[index] == "--root"
			options.prefix = args[index+1]
			index++
		case "--format":
			if index+1 >= len(args) || formatSet {
				return installPreflightOptions{}, errors.New("preflight format is invalid")
			}
			options.format = args[index+1]
			formatSet = true
			index++
		default:
			return installPreflightOptions{}, errors.New("preflight received an unsupported argument")
		}
	}
	if options.prefix == "" {
		return installPreflightOptions{}, errors.New("preflight requires --prefix or --root")
	}
	if options.format != "human" && options.format != "json" {
		return installPreflightOptions{}, errors.New("preflight format must be human or json")
	}
	return options, nil
}
