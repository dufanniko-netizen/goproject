package mailer

import (
	"crypto/tls"
	"fmt"
	"html"
	"io"
	"mime"
	"net"
	"net/mail"
	"net/smtp"
	"strconv"
	"strings"
	"time"

	"project-management/internal/config"
)

// ProjectApprovalNotification 是自动化项目提交审核邮件所需的数据。
type ProjectApprovalNotification struct {
	RecipientEmail string
	RecipientName  string
	ProjectName    string
	SubmitterName  string
	TaskCount      int64
	ProjectURL     string
	StartDate      string
	EndDate        string
}

// SendProjectApproval 发送自动化项目待审核通知。
func SendProjectApproval(notification ProjectApprovalNotification) error {
	cfg := config.AppConfig.Email
	if !cfg.Enabled {
		return nil
	}
	if strings.TrimSpace(notification.RecipientEmail) == "" {
		return fmt.Errorf("审核人未配置邮箱")
	}
	subject := fmt.Sprintf("[待审核] %s 提交了自动化项目：%s", notification.SubmitterName, notification.ProjectName)
	body := buildProjectApprovalHTML(notification)
	return sendHTML(cfg, notification.RecipientEmail, subject, body)
}

func buildProjectApprovalHTML(n ProjectApprovalNotification) string {
	escape := html.EscapeString
	recipient := escape(n.RecipientName)
	if recipient == "" {
		recipient = "您好"
	} else {
		recipient += "，您好"
	}
	return fmt.Sprintf(`<!doctype html><html><body style="font-family:Arial,'Microsoft YaHei',sans-serif;color:#1f2329;line-height:1.7"><div style="max-width:680px;margin:0 auto;padding:24px"><h2 style="color:#1677ff">自动化项目待审核提醒</h2><p>%s：</p><p><strong>%s</strong> 向您提交了一个自动化项目，请尽快登录系统审核。</p><table style="border-collapse:collapse;width:100%%;margin:18px 0"><tr><td style="border:1px solid #ddd;padding:10px;background:#fafafa;width:150px">项目名称</td><td style="border:1px solid #ddd;padding:10px">%s</td></tr><tr><td style="border:1px solid #ddd;padding:10px;background:#fafafa">提交人</td><td style="border:1px solid #ddd;padding:10px">%s</td></tr><tr><td style="border:1px solid #ddd;padding:10px;background:#fafafa">项目任务数</td><td style="border:1px solid #ddd;padding:10px">%d</td></tr><tr><td style="border:1px solid #ddd;padding:10px;background:#fafafa">开始时间</td><td style="border:1px solid #ddd;padding:10px">%s</td></tr><tr><td style="border:1px solid #ddd;padding:10px;background:#fafafa">结束时间</td><td style="border:1px solid #ddd;padding:10px">%s</td></tr></table><p><a href="%s" style="display:inline-block;padding:10px 18px;background:#1677ff;color:#fff;text-decoration:none;border-radius:4px">进入系统审核项目</a></p><p style="margin-top:28px;color:#8c8c8c;font-size:13px">此邮件由项目管理系统自动发送，请勿直接回复。</p></div></body></html>`, recipient, escape(n.SubmitterName), escape(n.ProjectName), escape(n.SubmitterName), n.TaskCount, escape(emptyAsDash(n.StartDate)), escape(emptyAsDash(n.EndDate)), escape(n.ProjectURL))
}

func emptyAsDash(value string) string {
	if strings.TrimSpace(value) == "" {
		return "-"
	}
	return value
}

func sendHTML(cfg config.EmailConfig, recipient, subject, body string) error {
	if cfg.Host == "" || cfg.Port <= 0 || cfg.FromAddress == "" {
		return fmt.Errorf("邮件SMTP配置不完整")
	}
	timeout := time.Duration(cfg.TimeoutSeconds) * time.Second
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	address := net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port))
	dialer := &net.Dialer{Timeout: timeout}
	tlsConfig := &tls.Config{ServerName: cfg.Host, MinVersion: tls.VersionTLS12}

	var client *smtp.Client
	if strings.EqualFold(cfg.TLSMode, "implicit") {
		connection, err := tls.DialWithDialer(dialer, "tcp", address, tlsConfig)
		if err != nil {
			return fmt.Errorf("连接SMTP服务器失败: %w", err)
		}
		client, err = smtp.NewClient(connection, cfg.Host)
		if err != nil {
			connection.Close()
			return fmt.Errorf("创建SMTP客户端失败: %w", err)
		}
	} else {
		connection, err := dialer.Dial("tcp", address)
		if err != nil {
			return fmt.Errorf("连接SMTP服务器失败: %w", err)
		}
		client, err = smtp.NewClient(connection, cfg.Host)
		if err != nil {
			connection.Close()
			return fmt.Errorf("创建SMTP客户端失败: %w", err)
		}
		if !strings.EqualFold(cfg.TLSMode, "none") {
			if ok, _ := client.Extension("STARTTLS"); !ok {
				client.Close()
				return fmt.Errorf("SMTP服务器不支持STARTTLS")
			}
			if err := client.StartTLS(tlsConfig); err != nil {
				client.Close()
				return fmt.Errorf("启用STARTTLS失败: %w", err)
			}
		}
	}
	defer client.Close()

	if cfg.Username != "" {
		if err := client.Auth(smtp.PlainAuth("", cfg.Username, cfg.Password, cfg.Host)); err != nil {
			return fmt.Errorf("SMTP认证失败: %w", err)
		}
	}
	if err := client.Mail(cfg.FromAddress); err != nil {
		return fmt.Errorf("设置发件人失败: %w", err)
	}
	if err := client.Rcpt(recipient); err != nil {
		return fmt.Errorf("设置收件人失败: %w", err)
	}
	w, err := client.Data()
	if err != nil {
		return fmt.Errorf("创建邮件正文失败: %w", err)
	}
	message := buildMessage(cfg, recipient, subject, body)
	if _, err := io.WriteString(w, message); err != nil {
		w.Close()
		return fmt.Errorf("写入邮件正文失败: %w", err)
	}
	if err := w.Close(); err != nil {
		return fmt.Errorf("发送邮件失败: %w", err)
	}
	if err := client.Quit(); err != nil {
		return fmt.Errorf("结束SMTP会话失败: %w", err)
	}
	return nil
}

func buildMessage(cfg config.EmailConfig, recipient, subject, body string) string {
	from := (&mail.Address{Name: cfg.FromName, Address: cfg.FromAddress}).String()
	to := (&mail.Address{Address: recipient}).String()
	encodedSubject := mime.QEncoding.Encode("UTF-8", subject)
	return "From: " + from + "\r\n" +
		"To: " + to + "\r\n" +
		"Subject: " + encodedSubject + "\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: text/html; charset=UTF-8\r\n" +
		"Content-Transfer-Encoding: 8bit\r\n\r\n" + body
}
