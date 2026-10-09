package launcher

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/KanaDoodle/CampusTrace/internal/practice"
)

func (m *manager) practice(ctx context.Context, args []string) error {
	if len(args) != 1 || (args[0] != "start" && args[0] != "stop" && args[0] != "status") {
		return errors.New("用法：campustrace practice start|stop|status")
	}
	if err := m.check(ctx); err != nil {
		return err
	}
	unlock, err := acquire(m.root)
	if err != nil {
		return err
	}
	defer unlock()
	switch args[0] {
	case "start":
		image := practice.Image
		if raw, e := m.capture(ctx, m.compose("--profile", "practice", "config", "--format", "json")...); e == nil {
			var cfg struct {
				Services map[string]struct {
					Environment map[string]string `json:"environment"`
				} `json:"services"`
			}
			if json.Unmarshal(raw, &cfg) == nil {
				if v := cfg.Services["sandbox-runner"].Environment["PRACTICE_IMAGE"]; v != "" {
					image = v
				}
			}
		}
		if _, e := m.capture(ctx, "docker", "image", "inspect", "campustrace-app:local"); e != nil {
			if e = m.build(ctx); e != nil {
				return e
			}
		}
		if _, err := m.capture(ctx, "docker", "image", "inspect", image); err != nil {
			if err = m.run(ctx, "docker", "pull", image); err != nil {
				return err
			}
		}
		if err = m.run(ctx, m.compose("--profile", "practice", "up", "--detach", "--no-deps", "--wait", "--wait-timeout", "45", "sandbox-runner")...); err != nil {
			return err
		}
		fmt.Fprintln(m.out, "Go 练习组件已启动。进入网页的「更多工具 → Go 练习」。")
		return nil
	case "stop":
		return m.run(ctx, m.compose("--profile", "practice", "stop", "sandbox-runner")...)
	default:
		return m.run(ctx, m.compose("--profile", "practice", "ps", "sandbox-runner")...)
	}
}
