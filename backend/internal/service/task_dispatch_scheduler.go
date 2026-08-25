package service

import (
	"bytes"
	"crypto/tls"
	"encoding/base64"
	"fmt"
	"io"
	"log"
	"mime"
	"mime/multipart"
	"mime/quotedprintable"
	"net/mail"
	"net/textproto"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/emersion/go-imap/v2"
	"github.com/emersion/go-imap/v2/imapclient"
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
	address := cfg.IMAPHost + ":" + strconv.Itoa(cfg.IMAPPort)
	client, err := imapclient.DialTLS(address, &imapclient.Options{TLSConfig: &tls.Config{ServerName: cfg.IMAPHost, MinVersion: tls.VersionTLS12}})
	if err != nil {
		return err
	}
	defer client.Close()
	if err := client.Login(cfg.Username, cfg.Password).Wait(); err != nil {
		return fmt.Errorf("IMAP认证失败: %w", err)
	}
	defer client.Logout().Wait()
	// 网易126/163邮箱要求第三方客户端在选择邮箱前通过RFC 2971 ID命令声明身份，
	// 否则即使登录成功也会以 Unsafe Login 拒绝 SELECT/EXAMINE INBOX。
	if _, err := client.ID(&imap.IDData{
		Name:    "GoProject Mail Collector",
		Version: "1.0",
		OS:      "Linux",
		Vendor:  "GoProject",
	}).Wait(); err != nil {
		return fmt.Errorf("发送IMAP客户端身份失败: %w", err)
	}
	if _, err := client.Select("INBOX", nil).Wait(); err != nil {
		return err
	}
	// 不只扫描未读邮件：网页邮箱或手机客户端可能在系统轮询前将回复标记为已读。
	// ImportWorkbook 会使用 Message-ID、UID、附件序号和批次状态保证重复扫描幂等。
	search, err := client.UIDSearch(&imap.SearchCriteria{Since: time.Now().AddDate(0, 0, -14)}, nil).Wait()
	if err != nil {
		return err
	}
	uids := search.AllUIDs()
	if len(uids) == 0 {
		return nil
	}
	section := &imap.FetchItemBodySection{Peek: true}
	messages, err := client.Fetch(imap.UIDSetNum(uids...), &imap.FetchOptions{UID: true, Envelope: true, BodySection: []*imap.FetchItemBodySection{section}}).Collect()
	if err != nil {
		return err
	}
	for _, message := range messages {
		body := message.FindBodySection(section)
		if len(body) == 0 {
			continue
		}
		attachments, messageID, err := xlsxAttachments(body)
		if err != nil || len(attachments) == 0 {
			continue
		}
		processed := false
		for index, attachment := range attachments {
			source := fmt.Sprintf("imap:%s:%d:%d", messageID, message.UID, index)
			if _, err := ImportWorkbook(db, attachment, source); err != nil {
				log.Printf("回邮附件导入失败(uid=%d): %v", message.UID, err)
				continue
			}
			processed = true
		}
		if processed {
			flags := imap.StoreFlags{Op: imap.StoreFlagsAdd, Flags: []imap.Flag{imap.FlagSeen}, Silent: true}
			if err := client.Store(imap.UIDSetNum(message.UID), &flags, nil).Close(); err != nil {
				log.Printf("邮件标记已读失败(uid=%d): %v", message.UID, err)
			}
		}
	}
	return nil
}

func xlsxAttachments(raw []byte) ([][]byte, string, error) {
	message, err := mail.ReadMessage(bytes.NewReader(raw))
	if err != nil {
		return nil, "", err
	}
	messageID := strings.Trim(message.Header.Get("Message-ID"), "<>")
	attachments := make([][]byte, 0)
	if err := collectXLSX(textproto.MIMEHeader(message.Header), message.Body, &attachments); err != nil {
		return nil, messageID, err
	}
	return attachments, messageID, nil
}

func collectXLSX(header textproto.MIMEHeader, body io.Reader, output *[][]byte) error {
	mediaType, params, err := mime.ParseMediaType(header.Get("Content-Type"))
	if err != nil {
		mediaType = header.Get("Content-Type")
	}
	if strings.HasPrefix(mediaType, "multipart/") {
		reader := multipart.NewReader(body, params["boundary"])
		for {
			part, err := reader.NextPart()
			if err == io.EOF {
				return nil
			}
			if err != nil {
				return err
			}
			if err := collectXLSX(part.Header, part, output); err != nil {
				return err
			}
		}
	}
	_, disposition, _ := mime.ParseMediaType(header.Get("Content-Disposition"))
	filename := disposition["filename"]
	if filename == "" {
		filename = params["name"]
	}
	if !strings.EqualFold(filepath.Ext(filename), ".xlsx") {
		return nil
	}
	var decoded io.Reader = body
	switch strings.ToLower(strings.TrimSpace(header.Get("Content-Transfer-Encoding"))) {
	case "base64":
		decoded = base64.NewDecoder(base64.StdEncoding, body)
	case "quoted-printable":
		decoded = quotedprintable.NewReader(body)
	}
	data, err := io.ReadAll(io.LimitReader(decoded, 20*1024*1024+1))
	if err != nil {
		return err
	}
	if len(data) > 20*1024*1024 {
		return fmt.Errorf("附件超过20MB")
	}
	*output = append(*output, data)
	return nil
}
