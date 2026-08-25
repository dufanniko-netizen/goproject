package service

import (
	"bytes"
	"fmt"
	"html"
	"log"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/xuri/excelize/v2"
	"gorm.io/gorm"

	"project-management/internal/model"
	"project-management/pkg/mailer"
)

var statusText = map[string]string{"wait": "未开始", "doing": "进行中", "done": "已完成", "pause": "已暂停", "cancel": "已取消", "closed": "已延期"}
var statusCode = map[string]string{"未开始": "wait", "进行中": "doing", "已完成": "done", "已暂停": "pause", "已取消": "cancel", "已延期": "closed"}

// DispatchProject 按已启用外部联系人发送当前应下发的三级任务。
func DispatchProject(db *gorm.DB, projectID uint) ([]model.TaskDispatchBatch, error) {
	var project model.Project
	if err := db.First(&project, projectID).Error; err != nil {
		return nil, err
	}
	if project.ProjectType != "smart_warehouse" {
		return nil, fmt.Errorf("仅智慧仓储项目支持任务收发")
	}
	today := time.Now().Format("2006-01-02")
	var contacts []model.ExternalTaskContact
	if err := db.Where("project_id = ? AND enabled = ?", projectID, true).Find(&contacts).Error; err != nil {
		return nil, err
	}
	result := make([]model.TaskDispatchBatch, 0)
	for _, contact := range contacts {
		var existing int64
		db.Model(&model.TaskDispatchBatch{}).Where("project_id = ? AND contact_id = ? AND dispatch_date = ? AND status IN ?", projectID, contact.ID, today, []string{"pending", "sent", "received"}).Count(&existing)
		if existing > 0 {
			continue
		}
		var tasks []model.Task
		err := db.Where("project_id = ? AND node_type = ? AND external_contact_id = ? AND (status IN ? OR (status = ? AND date(start_date) = ?))", projectID, "task", contact.ID, []string{"doing", "closed"}, "wait", today).Order("id").Find(&tasks).Error
		if err != nil {
			return result, err
		}
		if len(tasks) == 0 {
			continue
		}
		batch := model.TaskDispatchBatch{Token: uuid.NewString(), ProjectID: projectID, ContactID: contact.ID, Status: "pending", TaskCount: len(tasks), DispatchDate: today}
		if err := db.Transaction(func(tx *gorm.DB) error {
			if err := tx.Create(&batch).Error; err != nil {
				return err
			}
			for _, task := range tasks {
				if err := tx.Create(&model.TaskDispatchItem{BatchID: batch.ID, TaskID: task.ID}).Error; err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return result, err
		}
		data, filename, err := BuildWorkbook(project, contact, batch, tasks)
		if err != nil {
			markBatchFailed(db, &batch, err)
			continue
		}
		body := fmt.Sprintf("<p>%s，您好：</p><p>附件为项目 <strong>%s</strong> 今日需要反馈的任务，共 %d 项。</p><p>请填写今日进度、任务状态和今日进展说明；如需调整日期，请填写申请日期及原因。请直接回复本邮件并保留 Excel 附件。</p><p>批次号：%s</p>", html.EscapeString(contact.Recipient), html.EscapeString(project.Name), len(tasks), batch.Token)
		if err := mailer.SendHTMLWithAttachment(contact.Email, "[任务反馈] "+project.Name+" - "+contact.Name, body, filename, data); err != nil {
			markBatchFailed(db, &batch, err)
			continue
		}
		for _, cc := range splitEmails(contact.CCEmails) {
			if err := mailer.SendHTMLWithAttachment(cc, "[抄送][任务反馈] "+project.Name+" - "+contact.Name, body, filename, data); err != nil {
				batch.ErrorMessage += "抄送 " + cc + " 失败: " + err.Error() + "; "
			}
		}
		now := time.Now()
		batch.Status, batch.SentAt = "sent", &now
		if err := db.Save(&batch).Error; err != nil {
			return result, err
		}
		result = append(result, batch)
	}
	return result, nil
}

func markBatchFailed(db *gorm.DB, batch *model.TaskDispatchBatch, err error) {
	batch.Status, batch.ErrorMessage = "failed", err.Error()
	_ = db.Save(batch).Error
}

func BuildWorkbook(project model.Project, contact model.ExternalTaskContact, batch model.TaskDispatchBatch, tasks []model.Task) ([]byte, string, error) {
	f := excelize.NewFile()
	defer f.Close()
	const sheet = "任务反馈"
	f.SetSheetName("Sheet1", sheet)
	headers := []string{"批次号", "任务ID", "项目", "乙方负责人", "任务名称", "原开始日期", "原结束日期", "今日进度(0-100)", "任务状态", "今日进展说明", "申请开始日期", "申请结束日期", "时间调整原因"}
	for i, header := range headers {
		name, _ := excelize.CoordinatesToCellName(i+1, 1)
		f.SetCellValue(sheet, name, header)
	}
	for row, task := range tasks {
		values := []any{batch.Token, task.ID, project.Name, contact.Name, task.Title, dateValue(task.StartDate), dateValue(task.EndDate), task.Progress, statusText[task.Status], "", "", "", ""}
		for i, value := range values {
			name, _ := excelize.CoordinatesToCellName(i+1, row+2)
			f.SetCellValue(sheet, name, value)
		}
	}
	f.SetColWidth(sheet, "A", "A", 38)
	f.SetColWidth(sheet, "B", "B", 10)
	f.SetColWidth(sheet, "C", "G", 20)
	f.SetColWidth(sheet, "H", "M", 18)
	f.AutoFilter(sheet, fmt.Sprintf("A1:M%d", len(tasks)+1), nil)
	f.SetPanes(sheet, &excelize.Panes{Freeze: true, YSplit: 1, TopLeftCell: "A2", ActivePane: "bottomLeft"})
	buf, err := f.WriteToBuffer()
	if err != nil {
		return nil, "", err
	}
	filename := fmt.Sprintf("任务反馈_%s_%s_%s.xlsx", safeFilename(project.Name), safeFilename(contact.Name), batch.Token)
	return buf.Bytes(), filename, nil
}

// ImportWorkbook 回收系统生成的 Excel；只有原批次中的任务才允许更新。
func ImportWorkbook(db *gorm.DB, data []byte, sourceMessage string) (*model.TaskDispatchBatch, error) {
	f, err := excelize.OpenReader(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	defer f.Close()
	rows, err := f.GetRows("任务反馈")
	if err != nil || len(rows) < 2 {
		return nil, fmt.Errorf("Excel内容为空或格式错误")
	}
	token := strings.TrimSpace(cell(rows[1], 0))
	var batch model.TaskDispatchBatch
	if err := db.Where("token = ?", token).First(&batch).Error; err != nil {
		return nil, fmt.Errorf("批次号无效")
	}
	if batch.Status == "received" {
		return &batch, nil
	}
	if sourceMessage != "" {
		var count int64
		db.Model(&model.TaskDispatchBatch{}).Where("source_message = ?", sourceMessage).Count(&count)
		if count > 0 {
			return &batch, nil
		}
	}
	createdRequestIDs := make([]uint, 0)
	progressUpdateTaskIDs := make([]uint, 0)
	err = db.Transaction(func(tx *gorm.DB) error {
		success, failed := 0, 0
		for _, row := range rows[1:] {
			id64, parseErr := strconv.ParseUint(strings.TrimSpace(cell(row, 1)), 10, 64)
			if parseErr != nil {
				failed++
				continue
			}
			var item model.TaskDispatchItem
			if tx.Where("batch_id = ? AND task_id = ?", batch.ID, uint(id64)).First(&item).Error != nil {
				failed++
				continue
			}
			var task model.Task
			if tx.First(&task, uint(id64)).Error != nil {
				failed++
				continue
			}
			if progress, parseErr := strconv.Atoi(strings.TrimSpace(cell(row, 7))); parseErr == nil && progress >= 0 && progress <= 100 {
				task.Progress = progress
			}
			if status := statusCode[strings.TrimSpace(cell(row, 8))]; status != "" {
				oldStatus := task.Status
				task.Status = status
				updateCompletionTime(&task, oldStatus)
			}
			if note := strings.TrimSpace(cell(row, 9)); note != "" {
				task.Description = note
				task.LatestUpdate = note
				now := time.Now()
				task.LatestUpdateAt = &now
				progressUpdateTaskIDs = append(progressUpdateTaskIDs, task.ID)
			}
			if err := tx.Save(&task).Error; err != nil {
				failed++
				continue
			}
			requestedStart, requestedEnd := parseDate(cell(row, 10)), parseDate(cell(row, 11))
			if datesChanged(task, requestedStart, requestedEnd) {
				if task.CounterpartID == nil || strings.TrimSpace(cell(row, 12)) == "" {
					failed++
					continue
				}
				request := model.TaskTimeChangeRequest{TaskID: task.ID, BatchID: batch.ID, RequestedStart: requestedStart, RequestedEnd: requestedEnd, Reason: strings.TrimSpace(cell(row, 12)), Status: "pending", ReviewerID: *task.CounterpartID}
				if err := tx.Create(&request).Error; err != nil {
					failed++
					continue
				}
				createdRequestIDs = append(createdRequestIDs, request.ID)
			}
			success++
		}
		now := time.Now()
		batch.Status, batch.ReceivedAt, batch.SourceMessage = "received", &now, sourceMessage
		batch.SuccessCount, batch.FailureCount = success, failed
		return tx.Save(&batch).Error
	})
	if err == nil {
		sendProgressUpdateReminders(db, progressUpdateTaskIDs)
		sendTimeChangeReminders(db, createdRequestIDs)
	}
	return &batch, err
}

func sendProgressUpdateReminders(db *gorm.DB, taskIDs []uint) {
	for _, taskID := range taskIDs {
		var task model.Task
		if err := db.Preload("Project").Preload("Counterpart").First(&task, taskID).Error; err != nil {
			log.Printf("读取任务进展提醒失败(task_id=%d): %v", taskID, err)
			continue
		}
		if task.Counterpart == nil || strings.TrimSpace(task.Counterpart.Email) == "" {
			log.Printf("任务对口人未配置邮箱，无法发送进展提醒(task_id=%d)", taskID)
			continue
		}
		name := strings.TrimSpace(task.Counterpart.Nickname)
		if name == "" {
			name = task.Counterpart.Username
		}
		body := fmt.Sprintf("<p>%s，您好：</p><p>您对口的任务 <strong>%s</strong> 收到新的今日进展反馈。</p><p>项目：%s</p><p>状态：%s</p><p>进度：%d%%</p><p>今日进展说明：%s</p><p style=\"color:#888\">请登录项目管理系统查看任务详情。此邮件由系统自动发送，请勿直接回复。</p>",
			html.EscapeString(name), html.EscapeString(task.Title), html.EscapeString(task.Project.Name), html.EscapeString(statusText[task.Status]), task.Progress, html.EscapeString(task.LatestUpdate))
		if err := mailer.SendHTML(task.Counterpart.Email, "[任务进展] "+task.Project.Name+" - "+task.Title, body); err != nil {
			log.Printf("发送任务进展提醒失败(task_id=%d): %v", taskID, err)
		}
	}
}

func sendTimeChangeReminders(db *gorm.DB, requestIDs []uint) {
	for _, requestID := range requestIDs {
		var request model.TaskTimeChangeRequest
		if err := db.Preload("Task").Preload("Task.Project").Preload("Reviewer").First(&request, requestID).Error; err != nil {
			log.Printf("读取时间变更提醒失败(request_id=%d): %v", requestID, err)
			continue
		}
		if strings.TrimSpace(request.Reviewer.Email) == "" {
			log.Printf("任务对口人未配置邮箱，无法发送时间变更提醒(request_id=%d, reviewer_id=%d)", requestID, request.ReviewerID)
			continue
		}
		body := fmt.Sprintf("<p>%s，您好：</p><p>任务 <strong>%s</strong> 收到时间变更申请，请登录项目管理系统，在“Excel 收发中心 → 时间变更审核”中处理。</p><p>项目：%s</p><p>申请开始日期：%s</p><p>申请结束日期：%s</p><p>原因：%s</p><p style=\"color:#888\">此邮件由系统自动发送，请勿直接回复。</p>",
			html.EscapeString(request.Reviewer.Nickname), html.EscapeString(request.Task.Title), html.EscapeString(request.Task.Project.Name), dateValue(request.RequestedStart), dateValue(request.RequestedEnd), html.EscapeString(request.Reason))
		if err := mailer.SendHTML(request.Reviewer.Email, "[待审核] 任务时间变更申请："+request.Task.Title, body); err != nil {
			log.Printf("发送时间变更提醒失败(request_id=%d): %v", requestID, err)
		}
	}
}

func updateCompletionTime(task *model.Task, oldStatus string) {
	if task.Status == "done" && oldStatus != "done" {
		now := time.Now()
		task.CompletedAt, task.Progress = &now, 100
	} else if task.Status != "done" {
		task.CompletedAt = nil
	}
}

func datesChanged(task model.Task, start, end *time.Time) bool {
	return (start != nil && !sameDate(task.StartDate, start)) || (end != nil && !sameDate(task.EndDate, end))
}

func sameDate(a, b *time.Time) bool {
	if a == nil || b == nil {
		return a == nil && b == nil
	}
	return a.Format("2006-01-02") == b.Format("2006-01-02")
}

func cell(row []string, index int) string {
	if index >= len(row) {
		return ""
	}
	return row[index]
}
func dateValue(value *time.Time) string {
	if value == nil {
		return ""
	}
	return value.Format("2006-01-02")
}
func parseDate(value string) *time.Time {
	value = strings.TrimSpace(value)
	for _, layout := range []string{"2006-01-02", "2006/1/2", "2006/01/02", "2006-1-2"} {
		if parsed, err := time.ParseInLocation(layout, value, time.Local); err == nil {
			return &parsed
		}
	}
	return nil
}
func safeFilename(value string) string {
	return strings.NewReplacer("/", "_", "\\", "_", ":", "_", "*", "_", "?", "_", "\"", "_", "<", "_", ">", "_", "|", "_").Replace(value)
}

func splitEmails(value string) []string {
	return strings.FieldsFunc(value, func(r rune) bool { return r == ',' || r == ';' || r == '，' || r == '；' })
}
