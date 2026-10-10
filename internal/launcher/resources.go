package launcher

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strings"
)

var containerID = regexp.MustCompile(`^[a-f0-9]{12,64}$`)

func (m *manager) resources(ctx context.Context) error {
	raw, err := m.capture(ctx, m.compose("ps", "--status", "running", "--quiet")...)
	if err != nil {
		return errors.New("无法读取本项目的组件，请检查 Docker 是否运行")
	}
	ids := strings.Fields(string(raw))
	if len(ids) == 0 {
		fmt.Fprintln(m.out, "CampusTrace 当前没有运行中的组件。")
		return nil
	}
	for _, id := range ids {
		if !containerID.MatchString(id) {
			return errors.New("组件编号无法核对，本次未查询其他容器")
		}
	}
	fmt.Fprintln(m.out, "本项目当前资源占用（瞬时采样）：")
	return m.run(ctx, append([]string{"docker", "stats", "--no-stream", "--format", "table {{.Name}}\t{{.CPUPerc}}\t{{.MemUsage}}"}, ids...)...)
}
