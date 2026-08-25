package api

import (
	"fmt"
	"io"
	"net/mail"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"project-management/internal/model"
	"project-management/internal/service"
	"project-management/internal/utils"
)

type TaskDispatchHandler struct{ db *gorm.DB }

func NewTaskDispatchHandler(db *gorm.DB) *TaskDispatchHandler { return &TaskDispatchHandler{db: db} }

type externalContactRequest struct {
	Name      string `json:"name" binding:"required"`
	Company   string `json:"company"`
	Recipient string `json:"recipient"`
	Email     string `json:"email" binding:"required"`
	CCEmails  string `json:"cc_emails"`
	Enabled   *bool  `json:"enabled"`
	Notes     string `json:"notes"`
}

func (h *TaskDispatchHandler) ListContacts(c *gin.Context) {
	projectID, ok := h.smartProjectAccess(c)
	if !ok {
		return
	}
	var contacts []model.ExternalTaskContact
	if err := h.db.Where("project_id = ?", projectID).Order("enabled DESC, name").Find(&contacts).Error; err != nil {
		utils.Error(c, 500, "读取乙方联系人失败")
		return
	}
	utils.Success(c, contacts)
}

func (h *TaskDispatchHandler) CreateContact(c *gin.Context) {
	projectID, ok := h.smartProjectAccess(c)
	if !ok {
		return
	}
	var req externalContactRequest
	if c.ShouldBindJSON(&req) != nil || !validEmail(req.Email) {
		utils.Error(c, 400, "联系人名称和有效邮箱为必填项")
		return
	}
	enabled := true
	if req.Enabled != nil {
		enabled = *req.Enabled
	}
	contact := model.ExternalTaskContact{ProjectID: projectID, Name: strings.TrimSpace(req.Name), Company: strings.TrimSpace(req.Company), Recipient: strings.TrimSpace(req.Recipient), Email: strings.TrimSpace(req.Email), CCEmails: strings.TrimSpace(req.CCEmails), Enabled: enabled, Notes: strings.TrimSpace(req.Notes)}
	if err := h.db.Create(&contact).Error; err != nil {
		utils.Error(c, 409, "创建失败；同一项目中的联系人名称不能重复")
		return
	}
	utils.Success(c, contact)
}

func (h *TaskDispatchHandler) UpdateContact(c *gin.Context) {
	var contact model.ExternalTaskContact
	if h.db.First(&contact, c.Param("contact_id")).Error != nil {
		utils.Error(c, 404, "联系人不存在")
		return
	}
	if !utils.CheckProjectAccess(h.db, c, contact.ProjectID) && !utils.IsAdmin(c) {
		utils.Error(c, 403, "没有权限维护该联系人")
		return
	}
	var req externalContactRequest
	if c.ShouldBindJSON(&req) != nil || !validEmail(req.Email) {
		utils.Error(c, 400, "联系人名称和有效邮箱为必填项")
		return
	}
	contact.Name, contact.Company = strings.TrimSpace(req.Name), strings.TrimSpace(req.Company)
	contact.Recipient, contact.Email = strings.TrimSpace(req.Recipient), strings.TrimSpace(req.Email)
	contact.CCEmails, contact.Notes = strings.TrimSpace(req.CCEmails), strings.TrimSpace(req.Notes)
	if req.Enabled != nil {
		contact.Enabled = *req.Enabled
	}
	if err := h.db.Save(&contact).Error; err != nil {
		utils.Error(c, 409, "保存失败；请检查联系人名称是否重复")
		return
	}
	utils.Success(c, contact)
}

func (h *TaskDispatchHandler) DeleteContact(c *gin.Context) {
	var contact model.ExternalTaskContact
	if h.db.First(&contact, c.Param("contact_id")).Error != nil {
		utils.Error(c, 404, "联系人不存在")
		return
	}
	if !utils.CheckProjectAccess(h.db, c, contact.ProjectID) && !utils.IsAdmin(c) {
		utils.Error(c, 403, "没有权限维护该联系人")
		return
	}
	var count int64
	h.db.Model(&model.Task{}).Where("external_contact_id = ?", contact.ID).Count(&count)
	if count > 0 {
		contact.Enabled = false
		h.db.Save(&contact)
	} else {
		h.db.Delete(&contact)
	}
	utils.Success(c, gin.H{"disabled": count > 0})
}

func (h *TaskDispatchHandler) SendNow(c *gin.Context) {
	projectID, ok := h.smartProjectAccess(c)
	if !ok {
		return
	}
	batches, err := service.DispatchProject(h.db, projectID)
	if err != nil {
		utils.Error(c, 500, "下发失败："+err.Error())
		return
	}
	utils.Success(c, gin.H{"batches": batches, "count": len(batches)})
}

func (h *TaskDispatchHandler) ListBatches(c *gin.Context) {
	projectID, ok := h.smartProjectAccess(c)
	if !ok {
		return
	}
	var batches []model.TaskDispatchBatch
	if err := h.db.Preload("Contact").Where("project_id = ?", projectID).Order("id DESC").Limit(200).Find(&batches).Error; err != nil {
		utils.Error(c, 500, "读取收发记录失败")
		return
	}
	utils.Success(c, batches)
}

func (h *TaskDispatchHandler) ImportExcel(c *gin.Context) {
	file, err := c.FormFile("file")
	if err != nil || file.Size > 20*1024*1024 {
		utils.Error(c, 400, "请选择不超过20MB的xlsx文件")
		return
	}
	opened, err := file.Open()
	if err != nil {
		utils.Error(c, 400, "无法读取文件")
		return
	}
	defer opened.Close()
	data, err := io.ReadAll(opened)
	if err != nil {
		utils.Error(c, 400, "无法读取文件")
		return
	}
	batch, err := service.ImportWorkbook(h.db, data, fmt.Sprintf("manual:%d:%d", utils.GetUserID(c), time.Now().UnixNano()))
	if err != nil {
		utils.Error(c, 400, "导入失败："+err.Error())
		return
	}
	utils.Success(c, batch)
}

func (h *TaskDispatchHandler) ListTimeChangeRequests(c *gin.Context) {
	userID := utils.GetUserID(c)
	query := h.db.Preload("Task").Preload("Task.Project").Preload("Reviewer").Order("id DESC")
	if !utils.IsAdmin(c) {
		query = query.Where("reviewer_id = ?", userID)
	}
	if status := strings.TrimSpace(c.Query("status")); status != "" {
		query = query.Where("status = ?", status)
	}
	var requests []model.TaskTimeChangeRequest
	if err := query.Limit(300).Find(&requests).Error; err != nil {
		utils.Error(c, 500, "读取时间调整申请失败")
		return
	}
	utils.Success(c, requests)
}

func (h *TaskDispatchHandler) ReviewTimeChange(c *gin.Context) {
	var request model.TaskTimeChangeRequest
	if h.db.First(&request, c.Param("id")).Error != nil {
		utils.Error(c, 404, "申请不存在")
		return
	}
	if request.Status != "pending" {
		utils.Error(c, 409, "该申请已经处理")
		return
	}
	if request.ReviewerID != utils.GetUserID(c) && !utils.IsAdmin(c) {
		utils.Error(c, 403, "只有任务对口人可以审核")
		return
	}
	var req struct {
		Decision string `json:"decision" binding:"required"`
		Comment  string `json:"comment"`
	}
	if c.ShouldBindJSON(&req) != nil || (req.Decision != "approved" && req.Decision != "rejected") {
		utils.Error(c, 400, "decision必须为approved或rejected")
		return
	}
	err := h.db.Transaction(func(tx *gorm.DB) error {
		if req.Decision == "approved" {
			updates := map[string]any{}
			if request.RequestedStart != nil {
				updates["start_date"] = request.RequestedStart
			}
			if request.RequestedEnd != nil {
				updates["end_date"] = request.RequestedEnd
			}
			if len(updates) > 0 {
				if err := tx.Model(&model.Task{}).Where("id = ?", request.TaskID).Updates(updates).Error; err != nil {
					return err
				}
			}
		}
		now := time.Now()
		return tx.Model(&request).Updates(map[string]any{"status": req.Decision, "reviewed_at": &now, "review_comment": strings.TrimSpace(req.Comment)}).Error
	})
	if err != nil {
		utils.Error(c, 500, "审核保存失败")
		return
	}
	utils.Success(c, gin.H{"status": req.Decision})
}

func (h *TaskDispatchHandler) smartProjectAccess(c *gin.Context) (uint, bool) {
	projectID64, err := strconv.ParseUint(c.Param("id"), 10, 64)
	if err != nil {
		utils.Error(c, 400, "项目ID无效")
		return 0, false
	}
	projectID := uint(projectID64)
	var project model.Project
	if h.db.First(&project, projectID).Error != nil {
		utils.Error(c, 404, "项目不存在")
		return 0, false
	}
	if project.ProjectType != "smart_warehouse" {
		utils.Error(c, 400, "仅智慧仓储项目支持此功能")
		return 0, false
	}
	if !utils.CheckProjectAccess(h.db, c, projectID) && !utils.IsAdmin(c) {
		utils.Error(c, 403, "没有权限访问该项目")
		return 0, false
	}
	return projectID, true
}

func validEmail(value string) bool {
	address, err := mail.ParseAddress(strings.TrimSpace(value))
	return err == nil && address.Address == strings.TrimSpace(value)
}
