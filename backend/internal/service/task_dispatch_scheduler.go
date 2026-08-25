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

	"github.com/emersion/go-imap"
	imapid "github.com/emersion/go-imap-id"
	"github.com/emersion/go-imap/client"
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
	imapClient, err := client.DialTLS(address, &tls.Config{ServerName: cfg.IMAPHost, MinVersion: tls.VersionTLS12})
	if err != nil {
		return err
	}
	defer imapClient.Logout()
	if err := imapClient.Login(cfg.Username, cfg.Password); err != nil {
		return fmt.Errorf("IMAP认证失败: %w", err)
	}
	// 网易126/163邮箱要求第三方客户端在选择邮箱前通过RFC 2971 ID命令声明身份，
	// 否则即使登录成功也会以 Unsafe Login 拒绝 SELECT/EXAMINE INBOX。
	if _, err := imapid.NewClient(imapClient).ID(imapid.ID{
		imapid.FieldName:    "GoProject",
		imapid.FieldVersion: "1.0",
	}); err != nil {
		return fmt.Errorf("发送IMAP客户端身份失败: %w", err)
	}
	if _, err := imapClient.Select("INBOX", true); err != nil {
		return err
	}
	// 不只扫描未读邮件：网页邮箱或手机客户端可能在系统轮询前将回复标记为已读。
	// ImportWorkbook 会使用 Message-ID、UID、附件序号和批次状态保证重复扫描幂等。
	criteria := imap.NewSearchCriteria()
	criteria.Since = time.Now().AddDate(0, 0, -14)
	uids, err := imapClient.UidSearch(criteria)
	if err != nil {
		return err
	}
	if len(uids) == 0 {
		return nil
	}
	sequenceSet := new(imap.SeqSet)
	sequenceSet.AddNum(uids...)
	section := &imap.BodySectionName{Peek: true}
	messages := make(chan *imap.Message, len(uids))
	fetchDone := make(chan error, 1)
	go func() {
		fetchDone <- imapClient.UidFetch(sequenceSet, []imap.FetchItem{imap.FetchUid, section.FetchItem()}, messages)
	}()
	for message := range messages {
		body := message.GetBody(section)
		if body == nil {
			continue
		}
		raw, err := io.ReadAll(body)
		if err != nil {
			log.Printf("读取回邮失败(uid=%d): %v", message.Uid, err)
			continue
		}
		attachments, messageID, err := xlsxAttachments(raw)
		if err != nil || len(attachments) == 0 {
			continue
		}
		processed := false
		for index, attachment := range attachments {
			source := fmt.Sprintf("imap:%s:%d:%d", messageID, message.Uid, index)
			if _, err := ImportWorkbook(db, attachment, source); err != nil {
				log.Printf("回邮附件导入失败(uid=%d): %v", message.Uid, err)
				continue
			}
			processed = true
		}
		if processed {
			messageSet := new(imap.SeqSet)
			messageSet.AddNum(message.Uid)
			if err := imapClient.UidStore(messageSet, imap.FormatFlagsOp(imap.AddFlags, true), []interface{}{imap.SeenFlag}, nil); err != nil {
				log.Printf("邮件标记已读失败(uid=%d): %v", message.Uid, err)
			}
		}
	}
	return <-fetchDone
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
