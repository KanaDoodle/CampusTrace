package launcher

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
	"github.com/go-sql-driver/mysql"
	"io"
	"net"
	"os"
	"os/exec"
	"regexp"
	"strconv"
	"strings"
	"time"
)

type BackupMetadata struct {
	Format            int       `json:"format"`
	SHA256            string    `json:"sha256"`
	Bytes             int64     `json:"bytes"`
	SnapshotStartedAt time.Time `json:"snapshot_started_at"`
	CompletedAt       time.Time `json:"completed_at"`
	ServerVersion     string    `json:"server_version"`
	Collation         string    `json:"collation,omitempty"`
}
type RecoveryReport struct {
	Format            int             `json:"format"`
	SHA256            string          `json:"sha256"`
	Database          string          `json:"database"`
	CheckedAt         time.Time       `json:"checked_at"`
	DurationMS        int64           `json:"duration_ms"`
	SnapshotStartedAt time.Time       `json:"snapshot_started_at"`
	ServerVersion     string          `json:"server_version"`
	BeforeMigration   p.DatabaseProof `json:"before_migration"`
	AfterMigration    p.DatabaseProof `json:"after_migration"`
	Kept              bool            `json:"kept"`
	RecoveryChanges   map[string]int  `json:"recovery_changes,omitempty"`
}

func privateJSON(path string, v any) error {
	file, err := os.OpenFile(path, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(file)
	enc.SetIndent("", "  ")
	err = enc.Encode(v)
	closeErr := file.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		os.Remove(path)
	}
	return err
}
func fileDigest(file *os.File) (string, int64, error) {
	h := sha256.New()
	n, err := io.Copy(h, file)
	if err != nil {
		return "", 0, err
	}
	return hex.EncodeToString(h.Sum(nil)), n, nil
}
func backupMetadata(path string, started time.Time, server string, collation ...string) error {
	file, err := os.Open(path)
	if err != nil {
		return err
	}
	defer file.Close()
	hash, n, err := fileDigest(file)
	if err != nil {
		return err
	}
	coll := "utf8mb4_0900_ai_ci"
	if len(collation) > 0 {
		coll = collation[0]
	}
	return privateJSON(path+".meta.json", BackupMetadata{Format: 1, SHA256: hash, Bytes: n, SnapshotStartedAt: started, CompletedAt: time.Now().UTC(), ServerVersion: server, Collation: coll})
}
func openBackup(path string) (*os.File, BackupMetadata, error) {
	var meta BackupMetadata
	raw, err := os.ReadFile(path + ".meta.json")
	if err != nil {
		return nil, meta, errors.New("缺少备份校验文件 .meta.json；请使用新版 backup 重新生成备份")
	}
	if len(raw) > 8192 {
		return nil, meta, errors.New("备份校验文件过大")
	}
	if err = json.Unmarshal(raw, &meta); err != nil || meta.Format != 1 || meta.Bytes < 1 || len(meta.SHA256) != 64 {
		return nil, meta, errors.New("备份校验文件格式无效")
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, meta, err
	}
	info, err := file.Stat()
	if err != nil || !info.Mode().IsRegular() {
		file.Close()
		return nil, meta, errors.New("备份不是普通文件")
	}
	hash, n, err := fileDigest(file)
	if err != nil || n != meta.Bytes || hash != meta.SHA256 {
		file.Close()
		return nil, meta, errors.New("备份完整性校验失败，本次未创建恢复数据库")
	}
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		file.Close()
		return nil, meta, err
	}
	return file, meta, nil
}

func (m *manager) input(ctx context.Context, args []string, in io.Reader, out io.Writer) error {
	if m.executeInput != nil {
		return m.executeInput(ctx, m.root, args, in, out, io.Discard)
	}
	cmd := exec.CommandContext(ctx, args[0], args[1:]...)
	cmd.Dir = m.root
	cmd.Stdin = in
	cmd.Stdout = out
	cmd.Stderr = io.Discard
	return cmd.Run()
}
func (m *manager) adminSQL(ctx context.Context, query string) (string, error) {
	var out bytes.Buffer
	err := m.input(ctx, m.compose("exec", "-T", "mysql", "sh", "-c", `exec mysql -uroot -p"$MYSQL_ROOT_PASSWORD" --batch --skip-column-names`), strings.NewReader(query), &out)
	if err != nil {
		return "", errors.New("本机 MySQL 管理操作失败，请检查服务状态")
	}
	return strings.TrimSpace(out.String()), nil
}

type composeRecoveryConfig struct {
	Services map[string]struct {
		Ports       []struct{ Published string }
		Environment map[string]string
	}
}

func (m *manager) recoveryConfig(ctx context.Context) (composeRecoveryConfig, error) {
	var cfg composeRecoveryConfig
	raw, err := m.capture(ctx, m.compose("config", "--format", "json")...)
	if err != nil {
		return cfg, err
	}
	err = json.Unmarshal(raw, &cfg)
	return cfg, err
}
func (m *manager) liveStore(ctx context.Context) (*p.Store, error) {
	cfg, err := m.recoveryConfig(ctx)
	if err != nil {
		return nil, err
	}
	parsed, err := mysql.ParseDSN(cfg.Services["api"].Environment["MYSQL_DSN"])
	if err != nil {
		return nil, errors.New("无法读取数据库配置")
	}
	ports := cfg.Services["mysql"].Ports
	if len(ports) != 1 {
		return nil, errors.New("恢复检查需要 MySQL 发布一个本机端口")
	}
	port, e := strconv.Atoi(ports[0].Published)
	if e != nil || port < 1 || port > 65535 {
		return nil, errors.New("MySQL 端口无效")
	}
	parsed.Net = "tcp"
	parsed.Addr = net.JoinHostPort("127.0.0.1", ports[0].Published)
	return p.Open(ctx, parsed.FormatDSN())
}

var databaseIdentifier = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)

func (m *manager) databaseName(ctx context.Context) (string, error) {
	cfg, err := m.recoveryConfig(ctx)
	if err != nil {
		return "", err
	}
	parsed, err := mysql.ParseDSN(cfg.Services["api"].Environment["MYSQL_DSN"])
	if err != nil || !databaseIdentifier.MatchString(parsed.DBName) {
		return "", errors.New("数据库名称无效")
	}
	return parsed.DBName, nil
}
func mysqlCompatible(old, current string) bool {
	var a, b [3]int
	n, e := fmt.Sscanf(old, "%d.%d.%d", &a[0], &a[1], &a[2])
	if n != 3 || e != nil {
		return false
	}
	n, e = fmt.Sscanf(current, "%d.%d.%d", &b[0], &b[1], &b[2])
	if n != 3 || e != nil {
		return false
	}
	return a[0] == 8 && b[0] == 8 && (b[1] > a[1] || b[1] == a[1] && b[2] >= a[2])
}

// The importer has privileges only in its freshly-created schema. A USE,
// qualified INSERT or malicious dump cannot modify the live database.
func (m *manager) verifyBackup(ctx context.Context, path string, keep bool) (report RecoveryReport, result error) {
	started := time.Now()
	file, meta, err := openBackup(path)
	if err != nil {
		return report, err
	}
	defer file.Close()
	server, err := m.adminSQL(ctx, "SELECT VERSION();")
	if err != nil {
		return report, err
	}
	if !mysqlCompatible(meta.ServerVersion, server) {
		return report, errors.New("备份与当前 MySQL 版本不兼容（仅支持同为 MySQL 8 且恢复端版本不低于备份端）")
	}
	if meta.Collation == "" {
		meta.Collation = "utf8mb4_0900_ai_ci"
	}
	if !regexp.MustCompile(`^utf8mb4_[A-Za-z0-9_]+$`).MatchString(meta.Collation) {
		return report, errors.New("备份字符集不受支持")
	}
	id := d.ID()
	schema := "campustrace_restore_" + id
	username := "ct_restore_" + id[:16]
	password := d.ID()
	created, account, retained := false, false, false
	defer func() {
		clean, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		query := ""
		if account {
			query += "DROP USER IF EXISTS '" + username + "'@'%';"
		}
		if created && !retained {
			query += "DROP DATABASE `" + schema + "`;"
		}
		if query != "" {
			if _, e := m.adminSQL(clean, query); e != nil {
				result = errors.Join(result, fmt.Errorf("恢复检查清理未完成，请检查隔离库 %s", schema))
			}
		}
	}()
	if _, err = m.adminSQL(ctx, "CREATE DATABASE `"+schema+"` CHARACTER SET utf8mb4 COLLATE "+meta.Collation+";"); err != nil {
		return report, err
	}
	created = true
	if _, err = m.adminSQL(ctx, "CREATE USER '"+username+"'@'%' IDENTIFIED BY '"+password+"';"); err != nil {
		return report, err
	}
	account = true
	if _, err = m.adminSQL(ctx, "GRANT ALL ON `"+schema+"`.* TO '"+username+"'@'%';"); err != nil {
		return report, err
	}
	args := m.compose("exec", "-T", "-e", "MYSQL_PWD="+password, "mysql", "mysql", "--binary-mode=1", "--local-infile=0", "-u"+username, schema)
	if err = m.input(ctx, args, file, io.Discard); err != nil {
		return report, errors.New("SQL 导入失败；只操作了新建隔离数据库，当前数据库未被覆盖")
	}
	// Recheck the exact open file after import; a modified backup cannot pass silently.
	if _, err = file.Seek(0, io.SeekStart); err != nil {
		return report, err
	}
	hash, n, err := fileDigest(file)
	if err != nil || hash != meta.SHA256 || n != meta.Bytes {
		return report, errors.New("备份在恢复过程中发生变化，本次恢复不予保留")
	}
	cfg, err := m.recoveryConfig(ctx)
	if err != nil {
		return report, err
	}
	ports := cfg.Services["mysql"].Ports
	if len(ports) != 1 {
		return report, errors.New("缺少 MySQL 本机端口")
	}
	port, e := strconv.Atoi(ports[0].Published)
	if e != nil || port < 1 || port > 65535 {
		return report, errors.New("MySQL 本机端口无效")
	}
	dbcfg := mysql.NewConfig()
	dbcfg.User = username
	dbcfg.Passwd = password
	dbcfg.Net = "tcp"
	dbcfg.Addr = net.JoinHostPort("127.0.0.1", ports[0].Published)
	dbcfg.DBName = schema
	store, err := p.Open(ctx, dbcfg.FormatDSN())
	if err != nil {
		return report, errors.New("无法连接隔离恢复数据库")
	}
	defer store.DB.Close()
	report = RecoveryReport{Format: 1, SHA256: meta.SHA256, Database: schema, SnapshotStartedAt: meta.SnapshotStartedAt, ServerVersion: server, Kept: keep}
	report.BeforeMigration, err = store.ProofDatabase(ctx)
	if err != nil {
		return report, err
	}
	if len(report.BeforeMigration.Tables) == 0 {
		return report, errors.New("备份数据库为空")
	}
	if err = p.CheckMigrations(report.BeforeMigration.Migrations); err != nil {
		return report, err
	}
	// Require original core tables. Migrate must not turn an empty/wrong dump into success.
	for _, table := range []string{"users", "profiles", "projects", "project_facts", "jobs", "observations", "evidence", "assessments", "applications", "outbox", "completed_tasks"} {
		if _, ok := report.BeforeMigration.Tables[table]; !ok {
			return report, fmt.Errorf("备份缺少核心表 %s", table)
		}
	}
	if err = store.Migrate(ctx); err != nil {
		return report, errors.New("隔离数据库迁移失败，当前数据库未改动")
	}
	report.AfterMigration, err = store.ProofDatabase(ctx)
	if err != nil {
		return report, err
	}
	if keep {
		report.RecoveryChanges, err = store.PrepareRecovery(ctx)
		if err != nil {
			return report, err
		}
		if _, err = m.adminSQL(ctx, "GRANT ALL ON `"+schema+"`.* TO 'campus'@'%';"); err != nil {
			return report, err
		}
	}
	report.CheckedAt = time.Now().UTC()
	report.DurationMS = time.Since(started).Milliseconds()
	reportPath := path + ".verify-" + id[:12] + ".json"
	if err = privateJSON(reportPath, report); err != nil {
		return report, err
	}
	if keep {
		// Keep only the schema; disposable import credentials are always removed.
		retained = true
	}
	fmt.Fprintf(m.out, "备份恢复验证通过，耗时 %d ms；验证报告：%s\n", report.DurationMS, reportPath)
	if keep {
		fmt.Fprintf(m.out, "已保留恢复副本：%s。当前服务仍使用原数据库，切换步骤见 docs/recovery.md。\n", schema)
	}
	return report, nil
}

func (m *manager) retention(ctx context.Context, days int, apply bool) error {
	store, err := m.liveStore(ctx)
	if err != nil {
		return err
	}
	defer store.DB.Close()
	if apply {
		path, e := m.backup(ctx, "")
		if e != nil {
			return e
		}
		fmt.Fprintln(m.out, "清理前备份：", path)
		if _, e = m.verifyBackup(ctx, path, false); e != nil {
			return e
		}
	}
	report, err := store.RetainHistory(ctx, time.Now().UTC().Add(-time.Duration(days)*24*time.Hour), apply)
	if err != nil {
		return err
	}
	enc := json.NewEncoder(m.out)
	enc.SetIndent("", "  ")
	return enc.Encode(report)
}
