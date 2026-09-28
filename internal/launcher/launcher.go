// Package launcher manages the local Compose stack without storing model keys
// or coupling service lifetimes to the CLI process.
package launcher

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"
)

var appServices = []string{"api", "worker", "analysis-1", "analysis-2"}
var allServices = []string{"mysql", "redis", "etcd", "api", "worker", "analysis-1", "analysis-2"}

type Container struct {
	Service string
	State   string
	Health  string
}
type manager struct {
	root        string
	out, errOut io.Writer
	execute     func(context.Context, string, []string, io.Writer, io.Writer) error
}

func command(ctx context.Context, dir string, args []string, out, errOut io.Writer) error {
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = dir
	cmd.Stdout = out
	cmd.Stderr = errOut
	cmd.Stdin = os.Stdin
	return cmd.Run()
}
func (m *manager) compose(args ...string) []string {
	return append([]string{"docker", "compose", "--project-name", "campustrace", "--file", filepath.Join(m.root, "docker-compose.yml"), "--file", filepath.Join(m.root, "compose.app.yml")}, args...)
}
func (m *manager) run(ctx context.Context, args ...string) error {
	return m.execute(ctx, m.root, args, m.out, m.errOut)
}
func (m *manager) capture(ctx context.Context, args ...string) ([]byte, error) {
	var out, errOut bytes.Buffer
	err := m.execute(ctx, m.root, args, &out, &errOut)
	return out.Bytes(), err
}
func project(dir string) (string, error) {
	if dir != "" {
		absolute, err := filepath.Abs(dir)
		if err != nil {
			return "", err
		}
		if validProject(absolute) {
			return absolute, nil
		}
		return "", errors.New("指定目录缺少 docker-compose.yml 或 compose.app.yml")
	}
	cwd, err := os.Getwd()
	if err != nil {
		return "", err
	}
	candidates := []string{cwd}
	if exe, err := os.Executable(); err == nil {
		if resolved, err := filepath.EvalSymlinks(exe); err == nil {
			exe = resolved
		}
		candidates = append(candidates, filepath.Dir(exe))
	}
	for _, candidate := range candidates {
		for {
			if validProject(candidate) {
				return candidate, nil
			}
			parent := filepath.Dir(candidate)
			if parent == candidate {
				break
			}
			candidate = parent
		}
	}
	return "", errors.New("没有找到 CampusTrace 项目，请在项目目录运行，或使用 --dir 指定目录")
}
func validProject(dir string) bool {
	for _, name := range []string{"docker-compose.yml", "compose.app.yml"} {
		info, err := os.Stat(filepath.Join(dir, name))
		if err != nil || info.IsDir() {
			return false
		}
	}
	return true
}
func decodeContainers(raw []byte) ([]Container, error) {
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil, nil
	}
	var rows []Container
	if raw[0] == '[' {
		err := json.Unmarshal(raw, &rows)
		return rows, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	for {
		var row Container
		err := dec.Decode(&row)
		if err == io.EOF {
			return rows, nil
		}
		if err != nil {
			return nil, err
		}
		rows = append(rows, row)
	}
}
func (m *manager) states(ctx context.Context) ([]Container, error) {
	raw, err := m.capture(ctx, m.compose("ps", "--all", "--format", "json")...)
	if err != nil {
		return nil, errors.New("无法读取服务状态，请检查 Docker 是否运行")
	}
	return decodeContainers(raw)
}
func (m *manager) check(ctx context.Context) error {
	if _, err := m.capture(ctx, "docker", "compose", "version"); err != nil {
		return errors.New("未找到可用的 Docker Compose，请安装并启动 Docker Desktop / Docker Engine")
	}
	if _, err := m.capture(ctx, "docker", "info", "--format", "{{.ServerVersion}}"); err != nil {
		return errors.New("Docker 尚未运行，请先启动 Docker，再运行 CampusTrace")
	}
	if _, err := m.capture(ctx, m.compose("config", "--quiet")...); err != nil {
		return errors.New("Compose 配置无法读取，请检查项目文件及本地 .env 配置")
	}
	return nil
}
func (m *manager) port(ctx context.Context) (string, error) {
	raw, err := m.capture(ctx, m.compose("config", "--format", "json")...)
	if err != nil {
		return "", errors.New("无法检查网页端口配置")
	}
	var cfg struct {
		Services map[string]struct{ Ports []struct{ Published string } }
	}
	if err = json.Unmarshal(raw, &cfg); err != nil {
		return "", err
	}
	ports := cfg.Services["api"].Ports
	if len(ports) != 1 {
		return "", errors.New("网页端口配置无效")
	}
	n, err := strconv.Atoi(ports[0].Published)
	if err != nil || n < 1 || n > 65535 {
		return "", errors.New("网页端口必须是 1—65535 的整数")
	}
	return strconv.Itoa(n), nil
}
func occupied(port string) bool {
	connection, err := net.DialTimeout("tcp", net.JoinHostPort("127.0.0.1", port), time.Second)
	if err != nil {
		return false
	}
	connection.Close()
	return true
}
func (m *manager) legacy(ctx context.Context) bool {
	if runtime.GOOS != "darwin" {
		return false
	}
	raw, err := m.capture(ctx, "launchctl", "list", "com.campustrace.local")
	return err == nil && strings.Contains(string(raw), strconv.Quote(filepath.Join(m.root, "scripts", "start.sh")))
}
func (m *manager) stopLegacy(ctx context.Context) error {
	if !m.legacy(ctx) {
		return nil
	}
	leases, err := m.capture(ctx, m.compose("exec", "-T", "redis", "redis-cli", "--scan", "--pattern", "ct:matching:lease:*")...)
	if err != nil {
		return errors.New("无法确认旧服务的分析状态，本次未停止旧服务")
	}
	if len(bytes.TrimSpace(leases)) > 0 {
		return errors.New("旧服务正在深度分析，请等本轮完成后再切换启动方式")
	}
	fmt.Fprintln(m.out, "正在接管本项目原有的本机后台服务…")
	if err := m.run(ctx, "launchctl", "remove", "com.campustrace.local"); err != nil {
		return err
	}
	// The old supervisor's EXIT trap needs to finish before any new process starts.
	for i := 0; i < 30; i++ {
		if !m.legacy(ctx) {
			if _, err := os.Stat(filepath.Join(m.root, "bin", "api.pid")); errors.Is(err, os.ErrNotExist) {
				return nil
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(200 * time.Millisecond):
		}
	}
	return errors.New("旧服务尚未完全退出，请稍后再试")
}
func (m *manager) restoreLegacy() error {
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	return m.run(ctx, "launchctl", "submit", "-l", "com.campustrace.local", "-o", filepath.Join(m.root, "bin", "start.log"), "-e", filepath.Join(m.root, "bin", "start-error.log"), "--", "/usr/bin/env", "FOREGROUND=1", filepath.Join(m.root, "scripts", "start.sh"))
}
func (m *manager) build(ctx context.Context) error {
	fmt.Fprintln(m.out, "正在构建应用镜像；数据库和模型密钥不会放入镜像。首次构建需要下载依赖。")
	return m.run(ctx, m.compose("build", "api")...)
}
func acquire(root string) (func(), error) {
	dir := filepath.Join(root, "bin")
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, err
	}
	file, err := os.OpenFile(filepath.Join(dir, "cli.lock"), os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return nil, err
	}
	unlock, err := lockFile(file)
	if err != nil {
		file.Close()
		return nil, errors.New("另一个 CampusTrace 启停命令正在执行，请等待完成后再试")
	}
	return func() { unlock(); file.Close() }, nil
}
func (m *manager) start(ctx context.Context, rebuild, open bool, timeout int) (result error) {
	if err := m.check(ctx); err != nil {
		return err
	}
	before, err := m.states(ctx)
	if err != nil {
		return err
	}
	previous := map[string]bool{}
	appsRunning := false
	for _, row := range before {
		previous[row.Service] = row.State == "running"
	}
	for _, service := range appServices {
		appsRunning = appsRunning || previous[service]
	}
	port, err := m.port(ctx)
	if err != nil {
		return err
	}
	legacy := m.legacy(ctx)
	if occupied(port) && !previous["api"] && !legacy {
		return fmt.Errorf("网页端口 %s 已被其他程序或本机开发服务占用，请停止占用程序或在 .env 设置 CAMPUS_HTTP_PORT", port)
	}
	if _, err := m.capture(ctx, "docker", "image", "inspect", "campustrace-app:local", "--format", "{{.Id}}"); err != nil || rebuild {
		if (rebuild || appsRunning) && previous["mysql"] && !legacy {
			path, err := m.backup(ctx, "")
			if err != nil {
				return fmt.Errorf("更新前备份失败，原有服务未停止：%w", err)
			}
			fmt.Fprintln(m.out, "更新前备份：", path)
		}
		if err = m.build(ctx); err != nil {
			return errors.New("应用镜像构建失败，原有服务未停止；请检查上方构建信息")
		}
		if appsRunning && !legacy {
			if err = m.run(ctx, m.compose(append([]string{"stop"}, appServices...)...)...); err != nil {
				return errors.New("更新前旧应用未能全部停止，请运行 status 和 logs 检查")
			}
			for _, service := range appServices {
				previous[service] = false
			}
		}
	}
	if legacy {
		path, err := m.backup(ctx, "")
		if err != nil {
			return fmt.Errorf("切换前备份失败，原有服务未停止：%w", err)
		}
		fmt.Fprintln(m.out, "切换前备份：", path)
		if err = m.stopLegacy(ctx); err != nil {
			return err
		}
	}
	defer func() {
		if result == nil {
			return
		}
		rollback, cancel := context.WithTimeout(context.Background(), 40*time.Second)
		defer cancel()
		newServices := []string{}
		for _, service := range appServices {
			if !previous[service] {
				newServices = append(newServices, service)
			}
		}
		if len(newServices) > 0 {
			if err := m.run(rollback, m.compose(append([]string{"stop"}, newServices...)...)...); err != nil {
				result = errors.Join(result, errors.New("本次新启动的应用未能全部停止，请运行 status 检查"))
			}
		}
		if legacy {
			if err := m.restoreLegacy(); err != nil {
				result = errors.Join(result, errors.New("旧本机后台服务恢复失败，请查看 bin/start-error.log"))
			}
		}
	}()
	fmt.Fprintln(m.out, "正在启动整套服务并等待健康检查…")
	if err = m.run(ctx, m.compose("up", "--detach", "--wait", "--wait-timeout", strconv.Itoa(timeout))...); err != nil {
		return errors.New("服务未能全部就绪，数据卷保留。请运行 status、logs 或 doctor")
	}
	rows, err := m.states(ctx)
	if err != nil {
		return err
	}
	if err = ready(rows); err != nil {
		return err
	}
	address := "http://127.0.0.1:" + port
	fmt.Fprintln(m.out, "CampusTrace 已启动：", address)
	fmt.Fprintln(m.out, "关闭终端后服务继续运行。首次使用请在网页注册账号；已有账号和数据继续沿用。")
	if open {
		if err = m.open(ctx, address); err != nil {
			fmt.Fprintln(m.errOut, "浏览器未能自动打开，请手动访问：", address)
		}
	}
	return nil
}
func ready(rows []Container) error {
	byName := map[string]Container{}
	for _, row := range rows {
		byName[row.Service] = row
	}
	for _, name := range allServices {
		row, ok := byName[name]
		if !ok || row.State != "running" || row.Health != "healthy" {
			return fmt.Errorf("服务 %s 尚未就绪，请运行 logs %s 查看原因", name, name)
		}
	}
	return nil
}
func (m *manager) stop(ctx context.Context, all bool) error {
	if err := m.check(ctx); err != nil {
		return err
	}
	if err := m.stopLegacy(ctx); err != nil {
		return err
	}
	services := appServices
	if all {
		services = allServices
	}
	if err := m.run(ctx, m.compose(append([]string{"stop"}, services...)...)...); err != nil {
		return err
	}
	fmt.Fprintln(m.out, "服务已停止，岗位、求职资料和模型结果保留。停止依赖可使用 stop --all。")
	return nil
}
func (m *manager) status(ctx context.Context) error {
	if err := m.check(ctx); err != nil {
		return err
	}
	rows, err := m.states(ctx)
	if err != nil {
		return err
	}
	byName := map[string]Container{}
	for _, row := range rows {
		byName[row.Service] = row
	}
	for _, name := range allServices {
		row, ok := byName[name]
		state := "未启动"
		if ok {
			state = map[string]string{"running": "运行中", "exited": "已停止", "created": "已创建", "restarting": "重启中", "paused": "已暂停"}[row.State]
			if state == "" {
				state = row.State
			}
			if row.Health != "" {
				state += " / " + map[string]string{"healthy": "健康", "unhealthy": "异常", "starting": "检查中"}[row.Health]
			}
		}
		fmt.Fprintf(m.out, "%-12s %s\n", name, state)
	}
	if m.legacy(ctx) {
		fmt.Fprintln(m.out, "本项目仍有本机后台服务；start 会备份后接管。")
	}
	return ready(rows)
}
func (m *manager) backup(ctx context.Context, destination string) (string, error) {
	if destination == "" {
		destination = filepath.Join(m.root, "bin", "backups", "campustrace-"+time.Now().Format("20060102-150405.000000000")+".sql")
	}
	path, err := filepath.Abs(destination)
	if err != nil {
		return "", err
	}
	if err = os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		return "", err
	}
	file, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600)
	if err != nil {
		return "", errors.New("备份路径无法创建或文件已存在，本次不会覆盖旧备份")
	}
	args := m.compose("exec", "-T", "mysql", "sh", "-c", `exec mysqldump --default-character-set=utf8mb4 -uroot -p"$MYSQL_ROOT_PASSWORD" --single-transaction --no-tablespaces --set-gtid-purged=OFF campustrace`)
	err = m.execute(ctx, m.root, args, file, m.errOut)
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(path)
		return "", errors.New("数据库备份失败，请确认 MySQL 正在运行")
	}
	return path, nil
}
func (m *manager) open(ctx context.Context, address string) error {
	var args []string
	switch runtime.GOOS {
	case "darwin":
		args = []string{"open", address}
	case "windows":
		args = []string{"rundll32", "url.dll,FileProtocolHandler", address}
	default:
		args = []string{"xdg-open", address}
	}
	return m.run(ctx, args...)
}
func probe(ctx context.Context, address string) error {
	u, err := url.Parse(address)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.User != nil {
		return errors.New("健康检查只接受本机 HTTP 地址")
	}
	client := &http.Client{Timeout: 3 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	req, err := http.NewRequestWithContext(ctx, "GET", address, nil)
	if err != nil {
		return err
	}
	resp, err := client.Do(req)
	if err != nil {
		return errors.New("服务无法连接")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("服务尚未就绪（HTTP %d）", resp.StatusCode)
	}
	return nil
}

const help = `CampusTrace — 统一启动入口

  start [--open] [--build]  启动应用与 Docker 依赖；后台运行
  stop [--all]             停止应用；--all 同时停止依赖，保留数据
  restart [--build]        重启应用；--build 先构建更新再停旧服务
  status                  查看所有组件状态
  logs [组件] [--follow]   查看日志；组件为 api/worker/analysis-1/analysis-2/mysql/redis/etcd
  doctor                  检查 Docker、配置与服务状态
  backup [--file 路径]     将数据库备份到本机，文件权限仅当前用户可读写
  open                    打开网页

通用选项：--dir 项目目录。start/restart：--timeout 秒（默认 180）。
logs：--tail 行数（默认 100），选项可以放在组件名称前后。
首次启动需要 Docker；镜像不存在时自动构建，不添加样例岗位。
源代码更新后使用 restart --build。开发模式使用 make dev-run / make dev-stop。
`

func logArgs(args []string) []string {
	flags, positions := []string{}, []string{}
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if strings.HasPrefix(arg, "-") {
			flags = append(flags, arg)
			if (arg == "--dir" || arg == "-dir" || arg == "--tail" || arg == "-tail") && i+1 < len(args) {
				i++
				flags = append(flags, args[i])
			}
		} else {
			positions = append(positions, arg)
		}
	}
	return append(flags, positions...)
}

func Run(ctx context.Context, args []string, out, errOut io.Writer) error {
	if len(args) == 0 || args[0] == "help" || args[0] == "--help" || args[0] == "-h" {
		fmt.Fprint(out, help)
		return nil
	}
	if args[0] == "version" {
		fmt.Fprintln(out, "CampusTrace CLI v0.2")
		return nil
	}
	name := args[0]
	switch name {
	case "start", "stop", "restart", "status", "logs", "doctor", "backup", "open", "probe":
	default:
		return fmt.Errorf("未知命令 %q，运行 help 查看用法", name)
	}
	flags := flag.NewFlagSet(name, flag.ContinueOnError)
	flags.SetOutput(errOut)
	dir := flags.String("dir", "", "项目目录")
	var open, rebuild, all, follow bool
	timeout, tail := 180, 100
	destination, address := "", ""
	if name == "start" || name == "restart" {
		flags.BoolVar(&open, "open", false, "打开网页")
		flags.BoolVar(&rebuild, "build", false, "重建应用镜像")
		flags.IntVar(&timeout, "timeout", 180, "等待就绪的秒数")
	}
	if name == "stop" {
		flags.BoolVar(&all, "all", false, "同时停止依赖")
	}
	if name == "logs" {
		flags.BoolVar(&follow, "follow", false, "跟随日志")
		flags.IntVar(&tail, "tail", 100, "日志行数")
	}
	if name == "backup" {
		flags.StringVar(&destination, "file", "", "备份路径")
	}
	if name == "probe" {
		flags.StringVar(&address, "url", "", "本机健康检查地址")
	}
	flagArgs := args[1:]
	if name == "logs" {
		flagArgs = logArgs(flagArgs)
	}
	if err := flags.Parse(flagArgs); errors.Is(err, flag.ErrHelp) {
		return nil
	} else if err != nil {
		return err
	}
	if timeout < 1 || timeout > 600 || tail < 1 || tail > 10000 {
		return errors.New("timeout 范围 1—600 秒，tail 范围 1—10000 行")
	}
	extra := flags.Args()
	if name != "logs" && len(extra) > 0 {
		return errors.New("存在无法识别的参数，请运行 help 查看用法")
	}
	if name == "probe" {
		return probe(ctx, address)
	}
	root, err := project(*dir)
	if err != nil {
		return err
	}
	m := &manager{root: root, out: out, errOut: errOut, execute: command}
	if name == "start" || name == "stop" || name == "restart" {
		unlock, err := acquire(root)
		if err != nil {
			return err
		}
		defer unlock()
	}
	switch name {
	case "start":
		return m.start(ctx, rebuild, open, timeout)
	case "stop":
		return m.stop(ctx, all)
	case "restart":
		if err = m.check(ctx); err != nil {
			return err
		}
		if rebuild {
			if _, err = m.backup(ctx, ""); err != nil {
				return err
			}
			if err = m.build(ctx); err != nil {
				return err
			}
		}
		if err = m.stop(ctx, false); err != nil {
			return err
		}
		return m.start(ctx, false, open, timeout)
	case "status":
		return m.status(ctx)
	case "doctor":
		if err = m.check(ctx); err != nil {
			return err
		}
		fmt.Fprintln(out, "Docker 与 Compose 配置检查通过。项目目录：", root)
		return m.status(ctx)
	case "backup":
		if err = m.check(ctx); err != nil {
			return err
		}
		path, err := m.backup(ctx, destination)
		if err == nil {
			fmt.Fprintln(out, "本机备份：", path)
		}
		return err
	case "open":
		port, err := m.port(ctx)
		if err != nil {
			return err
		}
		if err = probe(ctx, "http://127.0.0.1:"+port+"/readyz"); err != nil {
			return errors.New("网页服务未启动，请先运行 start")
		}
		return m.open(ctx, "http://127.0.0.1:"+port)
	case "logs":
		if len(extra) > 1 {
			return errors.New("logs 最多指定一个组件")
		}
		if len(extra) == 1 {
			valid := false
			for _, service := range allServices {
				valid = valid || service == extra[0]
			}
			if !valid {
				return errors.New("未知日志组件，请运行 help 查看用法")
			}
		}
		if err = m.check(ctx); err != nil {
			return err
		}
		command := []string{"logs", "--tail", strconv.Itoa(tail)}
		if follow {
			command = append(command, "--follow")
		}
		return m.run(ctx, m.compose(append(command, extra...)...)...)
	}
	return nil
}
