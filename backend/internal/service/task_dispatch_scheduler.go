package service

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"gorm.io/gorm"

	"project-management/internal/config"
	"project-management/internal/model"
)

// StartTaskDispatchScheduler 启动智慧仓储任务的每日18点下发和回邮轮询。
func StartTaskDispatchScheduler(db *gorm.DB) {
	go func() {
		lastDispatchCheck := ""
		for {
			now := time.Now()
			if now.Hour() == 18 && lastDispatchCheck != now.Format("2006-01-02") {
				dispatchAllSmartProjects(db)
				lastDispatchCheck = now.Format("2006-01-02")
			}
			if config.AppConfig.Email.IMAPEnabled {
				if err := pollTaskReplyMailbox(db); err != nil {
					log.Printf("任务反馈邮箱轮询失败: %v", err)
				}
			}
			seconds := config.AppConfig.Email.IMAPPollSeconds
			if seconds < 30 {
				seconds = 60
			}
			time.Sleep(time.Duration(seconds) * time.Second)
		}
	}()
}

func dispatchAllSmartProjects(db *gorm.DB) {
	var projects []model.Project
	if err := db.Where("project_type = ?", "smart_warehouse").Find(&projects).Error; err != nil {
		log.Printf("读取智慧仓储项目失败: %v", err)
		return
	}
	for _, project := range projects {
		batches, err := DispatchProject(db, project.ID)
		if err != nil {
			log.Printf("项目 %d 自动下发失败: %v", project.ID, err)
			continue
		}
		if len(batches) > 0 {
			log.Printf("项目 %d 自动下发成功，共 %d 个联系人批次", project.ID, len(batches))
		}
	}
}

func pollTaskReplyMailbox(db *gorm.DB) error {
	cfg := config.AppConfig.Email
	if cfg.IMAPHost == "" || cfg.IMAPPort <= 0 || cfg.Username == "" || cfg.Password == "" {
		return fmt.Errorf("IMAP配置不完整")
	}

	// 网易126邮箱会对第三方客户端执行额外的客户端身份检查。服务器上的
	// Python imaplib 已验证能够完成 LOGIN、ID 和 SELECT INBOX，因此由它只
	// 负责下载 xlsx 附件；工作簿校验、权限判断和数据库更新仍由 Go 完成。
	outputDir, err := os.MkdirTemp("", "goproject-imap-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(outputDir)

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
	defer cancel()
	command := exec.CommandContext(ctx, "python3", "-c", neteaseIMAPCollectorScript)
	command.Env = append(os.Environ(),
		"GOPROJECT_IMAP_HOST="+cfg.IMAPHost,
		fmt.Sprintf("GOPROJECT_IMAP_PORT=%d", cfg.IMAPPort),
		"GOPROJECT_IMAP_USERNAME="+cfg.Username,
		"GOPROJECT_IMAP_PASSWORD="+cfg.Password,
		"GOPROJECT_IMAP_OUTPUT="+outputDir,
	)
	stdout, err := command.Output()
	if err != nil {
		if ctx.Err() != nil {
			return fmt.Errorf("IMAP收信超时: %w", ctx.Err())
		}
		if exitError, ok := err.(*exec.ExitError); ok {
			return fmt.Errorf("IMAP收信失败: %s", strings.TrimSpace(string(exitError.Stderr)))
		}
		return fmt.Errorf("启动Python IMAP收信器失败: %w", err)
	}

	type collectedAttachment struct {
		Path      string `json:"path"`
		MessageID string `json:"message_id"`
		UID       string `json:"uid"`
		Index     int    `json:"index"`
	}
	scanner := bufio.NewScanner(strings.NewReader(string(stdout)))
	for scanner.Scan() {
		var attachment collectedAttachment
		if err := json.Unmarshal(scanner.Bytes(), &attachment); err != nil {
			log.Printf("忽略无效的IMAP收信器输出: %v", err)
			continue
		}
		cleanPath := filepath.Clean(attachment.Path)
		cleanOutputDir := filepath.Clean(outputDir) + string(os.PathSeparator)
		if !strings.HasPrefix(cleanPath, cleanOutputDir) {
			log.Printf("忽略IMAP收信器返回的非法附件路径: %s", cleanPath)
			continue
		}
		workbook, err := os.ReadFile(cleanPath)
		if err != nil {
			log.Printf("读取回邮附件失败(uid=%s): %v", attachment.UID, err)
			continue
		}
		source := fmt.Sprintf("imap:%s:%s:%d", attachment.MessageID, attachment.UID, attachment.Index)
		batch, err := ImportWorkbook(db, workbook, source)
		if err != nil {
			log.Printf("回邮附件导入失败(uid=%s): %v", attachment.UID, err)
			continue
		}
		log.Printf("回邮附件导入成功(uid=%s, batch_id=%d, 成功=%d, 失败=%d)", attachment.UID, batch.ID, batch.SuccessCount, batch.FailureCount)
	}
	return scanner.Err()
}

const neteaseIMAPCollectorScript = `
import datetime
import email
import imaplib
import json
import os
import sys

imaplib.Commands["ID"] = ("AUTH",)
host = os.environ["GOPROJECT_IMAP_HOST"]
port = int(os.environ["GOPROJECT_IMAP_PORT"])
username = os.environ["GOPROJECT_IMAP_USERNAME"]
password = os.environ["GOPROJECT_IMAP_PASSWORD"]
output_dir = os.environ["GOPROJECT_IMAP_OUTPUT"]

client = imaplib.IMAP4_SSL(host, port)
try:
    client.login(username, password)
    status, _ = client._simple_command("ID", '("name" "GoProject" "version" "1.0" "vendor" "GoProject")')
    if status != "OK":
        raise RuntimeError("126邮箱拒绝IMAP客户端身份")
    status, detail = client.select("INBOX", readonly=True)
    if status != "OK":
        raise RuntimeError("无法选择收件箱: %r" % (detail,))
    since = (datetime.datetime.utcnow() - datetime.timedelta(days=14)).strftime("%d-%b-%Y")
    status, data = client.search(None, "SINCE", since)
    if status != "OK":
        raise RuntimeError("搜索收件箱失败: %r" % (data,))
    message_numbers = data[0].split()[-500:]
    for number in message_numbers:
        status, fetched = client.fetch(number, "(UID RFC822)")
        if status != "OK":
            continue
        raw = next((item[1] for item in fetched if isinstance(item, tuple) and len(item) > 1), None)
        if not raw:
            continue
        uid = number.decode("ascii", "ignore")
        for item in fetched:
            if isinstance(item, tuple) and item and isinstance(item[0], bytes):
                marker = item[0].decode("ascii", "ignore")
                if "UID " in marker:
                    uid = marker.split("UID ", 1)[1].split()[0].rstrip(")")
                    break
        message = email.message_from_bytes(raw)
        message_id = (message.get("Message-ID") or "").strip("<>")
        attachment_index = 0
        for part in message.walk():
            filename = part.get_filename()
            if not filename or not filename.lower().endswith(".xlsx"):
                continue
            payload = part.get_payload(decode=True)
            if not payload or len(payload) > 20 * 1024 * 1024:
                continue
            path = os.path.join(output_dir, "%s_%d.xlsx" % (uid, attachment_index))
            with open(path, "wb") as handle:
                handle.write(payload)
            print(json.dumps({
                "path": path,
                "message_id": message_id,
                "uid": uid,
                "index": attachment_index,
            }, ensure_ascii=True))
            attachment_index += 1
finally:
    try:
        client.logout()
    except Exception:
        pass
`
