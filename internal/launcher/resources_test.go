package launcher

import (
	"bytes"
	"context"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestResourcesQueriesOnlyContainersFromThisComposeProject(t *testing.T) {
	var out bytes.Buffer
	var calls [][]string
	ids := []string{strings.Repeat("a", 64), strings.Repeat("b", 64)}
	m := manager{root: t.TempDir(), out: &out, errOut: io.Discard}
	m.execute = func(_ context.Context, _ string, args []string, stdout, _ io.Writer) error {
		calls = append(calls, args)
		if args[1] == "compose" {
			io.WriteString(stdout, strings.Join(ids, "\n"))
		}
		return nil
	}
	if err := m.resources(context.Background()); err != nil {
		t.Fatal(err)
	}
	if len(calls) != 2 || !strings.Contains(strings.Join(calls[0], " "), "--project-name campustrace") || !reflect.DeepEqual(calls[1][5:], ids) || calls[1][1] != "stats" || calls[1][2] != "--no-stream" {
		t.Fatal(calls)
	}
}

func TestResourcesDoesNotFallBackToAllContainers(t *testing.T) {
	for _, output := range []string{"", "--all", "foreign-name"} {
		var calls int
		m := manager{root: t.TempDir(), out: io.Discard, errOut: io.Discard, execute: func(_ context.Context, _ string, args []string, stdout, _ io.Writer) error {
			calls++
			io.WriteString(stdout, output)
			return nil
		}}
		err := m.resources(context.Background())
		if calls != 1 || (output == "" && err != nil) || (output != "" && err == nil) {
			t.Fatal(output, calls, err)
		}
	}
}
