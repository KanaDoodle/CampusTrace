package launcher

import (
	"context"
	"errors"
	"io"
	"os/exec"
)

// Keep the production Compose command path, but direct its MySQL operations to
// the explicitly supplied, test-owned container. SQL/dump/import are real.
func ownedRecoveryCommand(ctx context.Context, container string, args []string, input io.Reader, out, errOut io.Writer) error {
	if len(args) < 3 || args[0] != "docker" || args[1] != "compose" || container == "" {
		return errors.New("expected a MySQL Compose fixture command")
	}
	index := -1
	for i, value := range args {
		if value == "exec" {
			index = i + 1
			break
		}
	}
	if index < 0 {
		return errors.New("unsupported fixture command")
	}
	command := []string{"exec", "-i"}
	for index < len(args) {
		switch args[index] {
		case "-T":
			index++
		case "-e":
			if index+1 >= len(args) {
				return errors.New("missing fixture environment value")
			}
			command = append(command, args[index:index+2]...)
			index += 2
		default:
			if args[index] != "mysql" || index+1 >= len(args) {
				return errors.New("only the owned MySQL service may be used")
			}
			command = append(command, container)
			command = append(command, args[index+1:]...)
			cmd := exec.CommandContext(ctx, "docker", command...)
			cmd.Stdin, cmd.Stdout, cmd.Stderr = input, out, errOut
			return cmd.Run()
		}
	}
	return errors.New("missing fixture service")
}
