package api

import (
	"fmt"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
	"project-management/internal/model"
	"project-management/internal/utils"
)

type TaskHandler struct {
	db *gorm.DB
}

// applySmartWarehouseCalculatedHours 计算智慧仓储任务的计划/实际天数及工时。
// 天数按自然日且包含首尾日期；尚未开始的任务实际天数为 0。
func applySmartWarehouseCalculatedHours(task *model.Task, now time.Time) {
	if task.Project.ProjectType != "smart_warehouse" {
		return
	}
	task.DueDate = nil
	task.PlannedDays = inclusiveDays(task.StartDate, task.EndDate)
	today := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.UTC)
	task.ActualDays = inclusiveDays(task.StartDate, &today)
	estimated := float64(task.PlannedDays * 8)
	actual := float64(task.ActualDays * 8)
	task.EstimatedHours = &estimated
	task.ActualHours = &actual
}

func inclusiveDays(start, end *time.Time) int {
	if start == nil || end == nil {
		return 0
	}
	startDay := time.Date(start.Year(), start.Month(), start.Day(), 0, 0, 0, 0, time.UTC)
	endDay := time.Date(end.Year(), end.Month(), end.Day(), 0, 0, 0, 0, time.UTC)
	if endDay.Before(startDay) {
		return 0
	}
	return int(endDay.Sub(startDay).Hours()/24) + 1
}

func updateTaskCompletionTime(task *model.Task, previousStatus string, now time.Time) {
	if task.Status == "done" {
		if previousStatus != "done" || task.CompletedAt == nil {
			completedAt := now
			task.CompletedAt = &completedAt
		}
		return
	}
	task.CompletedAt = nil
}

func (h *TaskHandler) ensureProjectNotUnderReview(c *gin.Context, projectID uint) error {
	var project model.Project
	if err := h.db.Select("id", "approval_status").First(&project, projectID).Error; err != nil {
		return err
	}
	if project.ApprovalStatus == "pending" && !utils.IsPendingProjectReviewer(h.db, c, projectID) {
		return fmt.Errorf("项目正在审核，任务暂时不能修改")
	}
	return nil
}

func NewTaskHandler(db *gorm.DB) *TaskHandler {
	return &TaskHandler{db: db}
}

// GetTasks 获取任务列表
func (h *TaskHandler) GetTasks(c *gin.Context) {
	var tasks []model.Task
	query := h.db.Preload("Project").Preload("Parent").Preload("Parent.Parent").Preload("Children").Preload("Requirement").Preload("Creator").Preload("Assignee").Preload("Counterpart").Preload("ExternalContact").Preload("Dependencies")

	// 权限过滤：普通用户只能看到自己创建或参与的任务
	query = utils.FilterTasksByUser(h.db, c, query)

	// 搜索
	if keyword := c.Query("keyword"); keyword != "" {
		query = query.Where("title LIKE ? OR description LIKE ? OR assignee_name LIKE ? OR counterpart_name LIKE ?", "%"+keyword+"%", "%"+keyword+"%", "%"+keyword+"%", "%"+keyword+"%")
	}

	// 项目筛选
	if projectID := c.Query("project_id"); projectID != "" {
		query = query.Where("project_id = ?", projectID)
	}

	// 状态筛选
	if status := c.Query("status"); status != "" {
		query = query.Where("status = ?", status)
	}

	// 优先级筛选
	if priority := c.Query("priority"); priority != "" {
		query = query.Where("priority = ?", priority)
	}

	// 负责人筛选
	if assigneeID := c.Query("assignee_id"); assigneeID != "" {
		query = query.Where("assignee_id = ?", assigneeID)
	}

	// 创建人筛选
	if creatorID := c.Query("creator_id"); creatorID != "" {
		query = query.Where("creator_id = ?", creatorID)
	}
	if nodeType := c.Query("node_type"); nodeType != "" {
		query = query.Where("node_type = ?", nodeType)
	}
	if c.Query("hide_historical_completed") == "true" {
		today := time.Now()
		startOfToday := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, today.Location())
		query = query.Where("status <> ? OR COALESCE(completed_at, updated_at) >= ?", "done", startOfToday)
	}

	// 分页
	page := utils.GetPage(c)
	pageSize := utils.GetPageSize(c)
	offset := (page - 1) * pageSize

	var total int64
	// 计算总数时需要应用与查询相同的筛选条件
	countQuery := utils.FilterTasksByUser(h.db, c, h.db.Model(&model.Task{}))

	// 搜索
	if keyword := c.Query("keyword"); keyword != "" {
		countQuery = countQuery.Where("title LIKE ? OR description LIKE ? OR assignee_name LIKE ? OR counterpart_name LIKE ?", "%"+keyword+"%", "%"+keyword+"%", "%"+keyword+"%", "%"+keyword+"%")
	}

	// 项目筛选
	if projectID := c.Query("project_id"); projectID != "" {
		countQuery = countQuery.Where("project_id = ?", projectID)
	}

	// 状态筛选
	if status := c.Query("status"); status != "" {
		countQuery = countQuery.Where("status = ?", status)
	}

	// 优先级筛选
	if priority := c.Query("priority"); priority != "" {
		countQuery = countQuery.Where("priority = ?", priority)
	}

	// 负责人筛选
	if assigneeID := c.Query("assignee_id"); assigneeID != "" {
		countQuery = countQuery.Where("assignee_id = ?", assigneeID)
	}

	// 创建人筛选
	if creatorID := c.Query("creator_id"); creatorID != "" {
		countQuery = countQuery.Where("creator_id = ?", creatorID)
	}
	if nodeType := c.Query("node_type"); nodeType != "" {
		countQuery = countQuery.Where("node_type = ?", nodeType)
	}
	if c.Query("hide_historical_completed") == "true" {
		today := time.Now()
		startOfToday := time.Date(today.Year(), today.Month(), today.Day(), 0, 0, 0, 0, today.Location())
		countQuery = countQuery.Where("status <> ? OR COALESCE(completed_at, updated_at) >= ?", "done", startOfToday)
	}

	countQuery.Count(&total)

	if err := query.Offset(offset).Limit(pageSize).Order("created_at DESC").Find(&tasks).Error; err != nil {
		utils.Error(c, utils.CodeError, "查询失败")
		return
	}
	for i := range tasks {
		applySmartWarehouseCalculatedHours(&tasks[i], time.Now())
	}

	utils.Success(c, gin.H{
		"list":      tasks,
		"total":     total,
		"page":      page,
		"page_size": pageSize,
	})
}

// GetTask 获取任务详情
func (h *TaskHandler) GetTask(c *gin.Context) {
	id := c.Param("id")
	var task model.Task
	if err := h.db.Preload("Project").Preload("Parent").Preload("Parent.Parent").Preload("Children").Preload("Requirement").Preload("Creator").Preload("Assignee").Preload("Counterpart").Preload("ExternalContact").Preload("Dependencies").First(&task, id).Error; err != nil {
		utils.Error(c, 404, "任务不存在")
		return
	}
	applySmartWarehouseCalculatedHours(&task, time.Now())

	// 权限检查：普通用户只能查看自己创建或参与的任务
	if !utils.CheckTaskReadAccess(h.db, c, task.ID) {
		utils.Error(c, 403, "没有权限访问该任务")
		return
	}

	utils.Success(c, task)
}

// CreateTask 创建任务
func (h *TaskHandler) CreateTask(c *gin.Context) {
	var req struct {
		Title             string   `json:"title" binding:"required"`
		Description       string   `json:"description"`
		Status            string   `json:"status"`
		Priority          string   `json:"priority"`
		ProjectID         uint     `json:"project_id" binding:"required"`
		RequirementID     *uint    `json:"requirement_id"`
		AssigneeID        *uint    `json:"assignee_id"`
		AssigneeName      string   `json:"assignee_name"`
		CounterpartID     *uint    `json:"counterpart_id"`
		CounterpartName   string   `json:"counterpart_name"`
		ExternalContactID *uint    `json:"external_contact_id"`
		StartDate         *string  `json:"start_date"`
		EndDate           *string  `json:"end_date"`
		DueDate           *string  `json:"due_date"`
		Progress          int      `json:"progress"`
		EstimatedHours    *float64 `json:"estimated_hours"`
		DependencyIDs     []uint   `json:"dependency_ids"`
		ParentID          *uint    `json:"parent_id"`
		TaskSequence      string   `json:"task_sequence"`
		Milestone1        string   `json:"milestone1"`
		Milestone2        string   `json:"milestone2"`
		Milestone3        string   `json:"milestone3"`
		CurrentNode       string   `json:"current_node"`
		PlanProgress      int      `json:"plan_progress"`
		ReasonAnalysis    string   `json:"reason_analysis"`
		RequiredSupport   string   `json:"required_support"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		utils.Error(c, 400, "参数错误")
		return
	}

	// 获取当前用户ID
	userID, exists := c.Get("user_id")
	if !exists {
		utils.Error(c, 401, "未登录")
		return
	}

	// 验证状态
	if req.Status == "" {
		req.Status = "wait"
	}
	validStatuses := map[string]bool{
		"wait":   true,
		"doing":  true,
		"done":   true,
		"pause":  true,
		"cancel": true,
		"closed": true,
	}
	if !validStatuses[req.Status] {
		utils.Error(c, 400, "状态值无效，有效值：wait, doing, done, pause, cancel, closed")
		return
	}

	// 验证优先级
	if req.Priority == "" {
		req.Priority = "medium"
	}
	validPriorities := map[string]bool{
		"low":    true,
		"medium": true,
		"high":   true,
		"urgent": true,
	}
	if !validPriorities[req.Priority] {
		utils.Error(c, 400, "优先级值无效")
		return
	}

	// 验证进度
	if req.Progress < 0 || req.Progress > 100 {
		utils.Error(c, 400, "进度值必须在0-100之间")
		return
	}
	if req.PlanProgress < 0 || req.PlanProgress > 100 {
		utils.Error(c, 400, "任务计划进度必须在0-100之间")
		return
	}

	// 验证项目是否存在
	var project model.Project
	if err := h.db.First(&project, req.ProjectID).Error; err != nil {
		utils.Error(c, 400, "项目不存在")
		return
	}
	if err := h.ensureProjectNotUnderReview(c, project.ID); err != nil {
		utils.Error(c, 409, err.Error())
		return
	}

	// 权限检查：普通用户只能在自己参与的项目中创建任务
	if !utils.CheckProjectAccess(h.db, c, project.ID) {
		utils.Error(c, 403, "没有权限在该项目中创建任务")
		return
	}

	level := 1
	nodeType := "group"
	if project.ProjectType == "automation" {
		req.ParentID = nil
		nodeType = "task"
	} else if req.ParentID != nil {
		var parent model.Task
		if err := h.db.First(&parent, *req.ParentID).Error; err != nil {
			utils.Error(c, 400, "父任务不存在")
			return
		}
		if parent.ProjectID != req.ProjectID {
			utils.Error(c, 400, "父任务必须属于同一项目")
			return
		}
		if parent.Level >= 3 {
			utils.Error(c, 400, "任务最多支持三级")
			return
		}
		level = parent.Level + 1
		if level == 3 {
			nodeType = "task"
		}
		// 只要拥有下级节点，父节点就是分类，不再作为执行任务参与甘特图。
		h.db.Model(&parent).Update("node_type", "group")
	}
	if project.ProjectType == "smart_warehouse" && nodeType == "task" && (req.CounterpartID == nil || *req.CounterpartID == 0) {
		utils.Error(c, 400, "智慧仓储具体任务必须选择系统用户作为对口人")
		return
	}

	// 如果指定了需求，验证需求是否存在且属于同一项目
	if req.RequirementID != nil {
		var requirement model.Requirement
		if err := h.db.First(&requirement, *req.RequirementID).Error; err != nil {
			utils.Error(c, 400, "需求不存在")
			return
		}
		if requirement.ProjectID != req.ProjectID {
			utils.Error(c, 400, "需求必须属于同一项目")
			return
		}
	}

	// 如果指定了负责人，验证用户是否存在
	if req.AssigneeID != nil && *req.AssigneeID != 0 {
		var user model.User
		if err := h.db.First(&user, *req.AssigneeID).Error; err != nil {
			utils.Error(c, 400, "负责人不存在")
			return
		}
	}
	if req.CounterpartID != nil && *req.CounterpartID != 0 {
		var user model.User
		if err := h.db.First(&user, *req.CounterpartID).Error; err != nil {
			utils.Error(c, 400, "对口人不存在")
			return
		}
	}
	if req.ExternalContactID != nil && *req.ExternalContactID != 0 {
		var contact model.ExternalTaskContact
		if err := h.db.Where("id = ? AND project_id = ? AND enabled = ?", *req.ExternalContactID, req.ProjectID, true).First(&contact).Error; err != nil {
			utils.Error(c, 400, "乙方联系人不存在、已停用或不属于当前项目")
			return
		}
		req.AssigneeName = contact.Name
		req.AssigneeID = nil
	}

	// 解析日期
	var startDate, endDate, dueDate *time.Time
	if req.StartDate != nil && *req.StartDate != "" {
		if t, err := time.Parse("2006-01-02", *req.StartDate); err == nil {
			startDate = &t
		}
	}
	if req.EndDate != nil && *req.EndDate != "" {
		if t, err := time.Parse("2006-01-02", *req.EndDate); err == nil {
			endDate = &t
		}
	}
	if req.DueDate != nil && *req.DueDate != "" {
		if t, err := time.Parse("2006-01-02", *req.DueDate); err == nil {
			dueDate = &t
		}
	}

	task := model.Task{
		Title:             req.Title,
		Description:       req.Description,
		Status:            req.Status,
		Priority:          req.Priority,
		ProjectID:         req.ProjectID,
		RequirementID:     req.RequirementID,
		CreatorID:         userID.(uint),
		AssigneeID:        req.AssigneeID,
		AssigneeName:      strings.TrimSpace(req.AssigneeName),
		CounterpartID:     req.CounterpartID,
		CounterpartName:   strings.TrimSpace(req.CounterpartName),
		ExternalContactID: req.ExternalContactID,
		StartDate:         startDate,
		EndDate:           endDate,
		DueDate:           dueDate,
		Progress:          req.Progress,
		EstimatedHours:    req.EstimatedHours,
		ParentID:          req.ParentID,
		Level:             level,
		NodeType:          nodeType,
		TaskSequence:      req.TaskSequence,
		Milestone1:        req.Milestone1,
		Milestone2:        req.Milestone2,
		Milestone3:        req.Milestone3,
		CurrentNode:       req.CurrentNode,
		PlanProgress:      req.PlanProgress,
		ReasonAnalysis:    req.ReasonAnalysis,
		RequiredSupport:   req.RequiredSupport,
	}
	if task.AssigneeID != nil && *task.AssigneeID == 0 {
		task.AssigneeID = nil
	}
	if task.CounterpartID != nil && *task.CounterpartID == 0 {
		task.CounterpartID = nil
	}
	if project.ProjectType == "smart_warehouse" {
		task.DueDate = nil
		task.Project = project
		applySmartWarehouseCalculatedHours(&task, time.Now())
		// 仅借助 ProjectType 计算字段，创建任务时不写回项目关联。
		task.Project = model.Project{}
	}
	updateTaskCompletionTime(&task, "", time.Now())

	if err := h.db.Create(&task).Error; err != nil {
		utils.Error(c, utils.CodeError, "创建失败")
		return
	}

	// 设置任务依赖关系
	if len(req.DependencyIDs) > 0 {
		var dependencies []model.Task
		if err := h.db.Where("id IN ?", req.DependencyIDs).Find(&dependencies).Error; err != nil {
			utils.Error(c, 400, "依赖任务不存在")
			return
		}
		// 检查循环依赖
		for _, depID := range req.DependencyIDs {
			if depID == task.ID {
				utils.Error(c, 400, "任务不能依赖自己")
				return
			}
		}
		if err := h.db.Model(&task).Association("Dependencies").Replace(dependencies); err != nil {
			utils.Error(c, utils.CodeError, "设置依赖失败")
			return
		}
	}

	// 重新加载关联数据
	h.db.Preload("Project").Preload("Parent").Preload("Parent.Parent").Preload("Children").Preload("Requirement").Preload("Creator").Preload("Assignee").Preload("Counterpart").Preload("ExternalContact").Preload("Dependencies").First(&task, task.ID)
	applySmartWarehouseCalculatedHours(&task, time.Now())

	// 记录创建操作
	if userID, exists := c.Get("user_id"); exists {
		dbValue, _ := c.Get("db")
		if db, ok := dbValue.(*gorm.DB); ok {
			utils.RecordAction(db, "task", task.ID, "created", userID.(uint), "", nil)
		}
	}

	utils.Success(c, task)
}

// UpdateTask 更新任务
func (h *TaskHandler) UpdateTask(c *gin.Context) {
	id := c.Param("id")
	var task model.Task
	if err := h.db.First(&task, id).Error; err != nil {
		utils.Error(c, 404, "任务不存在")
		return
	}

	// 权限检查：普通用户只能更新自己创建或参与的任务
	if !utils.CheckTaskAccess(h.db, c, task.ID) {
		utils.Error(c, 403, "没有权限更新该任务")
		return
	}
	if err := h.ensureProjectNotUnderReview(c, task.ProjectID); err != nil {
		utils.Error(c, 409, err.Error())
		return
	}

	// 保存旧对象用于比较
	oldTask := task

	var req struct {
		Title             *string  `json:"title"`
		Description       *string  `json:"description"`
		Status            *string  `json:"status"`
		Priority          *string  `json:"priority"`
		ProjectID         *uint    `json:"project_id"`
		RequirementID     *uint    `json:"requirement_id"`
		AssigneeID        *uint    `json:"assignee_id"`
		AssigneeName      *string  `json:"assignee_name"`
		CounterpartID     *uint    `json:"counterpart_id"`
		CounterpartName   *string  `json:"counterpart_name"`
		ExternalContactID *uint    `json:"external_contact_id"`
		StartDate         *string  `json:"start_date"`
		EndDate           *string  `json:"end_date"`
		DueDate           *string  `json:"due_date"`
		Progress          *int     `json:"progress"`
		EstimatedHours    *float64 `json:"estimated_hours"`
		ActualHours       *float64 `json:"actual_hours"` // 实际工时，会自动创建资源分配
		WorkDate          *string  `json:"work_date"`    // 工作日期（YYYY-MM-DD），用于资源分配
		DependencyIDs     *[]uint  `json:"dependency_ids"`
		ParentID          *uint    `json:"parent_id"`
		TaskSequence      *string  `json:"task_sequence"`
		Milestone1        *string  `json:"milestone1"`
		Milestone2        *string  `json:"milestone2"`
		Milestone3        *string  `json:"milestone3"`
		CurrentNode       *string  `json:"current_node"`
		PlanProgress      *int     `json:"plan_progress"`
		ReasonAnalysis    *string  `json:"reason_analysis"`
		RequiredSupport   *string  `json:"required_support"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		utils.Error(c, 400, "参数错误")
		return
	}

	// 更新字段
	if req.Title != nil {
		task.Title = *req.Title
	}
	if req.Description != nil {
		task.Description = *req.Description
	}
	if req.Status != nil {
		// 验证状态
		validStatuses := map[string]bool{
			"wait":   true,
			"doing":  true,
			"done":   true,
			"pause":  true,
			"cancel": true,
			"closed": true,
		}
		if !validStatuses[*req.Status] {
			utils.Error(c, 400, "状态值无效，有效值：wait, doing, done, pause, cancel, closed")
			return
		}
		task.Status = *req.Status
		updateTaskCompletionTime(&task, oldTask.Status, time.Now())
	}
	if req.Priority != nil {
		// 验证优先级
		validPriorities := map[string]bool{
			"low":    true,
			"medium": true,
			"high":   true,
			"urgent": true,
		}
		if !validPriorities[*req.Priority] {
			utils.Error(c, 400, "优先级值无效")
			return
		}
		task.Priority = *req.Priority
	}
	if req.ProjectID != nil {
		// 验证项目是否存在
		var project model.Project
		if err := h.db.First(&project, *req.ProjectID).Error; err != nil {
			utils.Error(c, 400, "项目不存在")
			return
		}
		task.ProjectID = *req.ProjectID
	}
	var taskProject model.Project
	if err := h.db.First(&taskProject, task.ProjectID).Error; err != nil {
		utils.Error(c, 400, "项目不存在")
		return
	}
	if taskProject.ProjectType == "automation" {
		task.ParentID = nil
		task.Level = 1
		task.NodeType = "task"
	} else if req.ParentID != nil {
		var childCount int64
		h.db.Model(&model.Task{}).Where("parent_id = ?", task.ID).Count(&childCount)
		parentChanged := (task.ParentID == nil && *req.ParentID != 0) ||
			(task.ParentID != nil && *task.ParentID != *req.ParentID)
		if parentChanged && childCount > 0 {
			utils.Error(c, 400, "该任务下存在子任务，请先调整子任务后再修改父任务")
			return
		}
		if *req.ParentID == 0 {
			task.ParentID = nil
			task.Level = 1
			task.NodeType = "group"
		} else {
			if *req.ParentID == task.ID {
				utils.Error(c, 400, "任务不能以自身作为父任务")
				return
			}
			var parent model.Task
			if err := h.db.First(&parent, *req.ParentID).Error; err != nil {
				utils.Error(c, 400, "父任务不存在")
				return
			}
			if parent.ProjectID != task.ProjectID || parent.Level >= 3 {
				utils.Error(c, 400, "父任务必须属于同一项目且只能选择一、二级任务")
				return
			}
			task.ParentID = req.ParentID
			task.Level = parent.Level + 1
			if task.Level == 3 {
				task.NodeType = "task"
			} else {
				task.NodeType = "group"
			}
			h.db.Model(&parent).Update("node_type", "group")
		}
	}
	if req.RequirementID != nil {
		// 验证需求是否存在且属于同一项目
		if *req.RequirementID != 0 {
			var requirement model.Requirement
			if err := h.db.First(&requirement, *req.RequirementID).Error; err != nil {
				utils.Error(c, 400, "需求不存在")
				return
			}
			if requirement.ProjectID != task.ProjectID {
				utils.Error(c, 400, "需求必须属于同一项目")
				return
			}
			task.RequirementID = req.RequirementID
		} else {
			task.RequirementID = nil
		}
	}
	if req.AssigneeID != nil {
		// 验证负责人是否存在
		if *req.AssigneeID != 0 {
			var user model.User
			if err := h.db.First(&user, *req.AssigneeID).Error; err != nil {
				utils.Error(c, 400, "负责人不存在")
				return
			}
			task.AssigneeID = req.AssigneeID
		} else {
			task.AssigneeID = nil
		}
	}
	if req.AssigneeName != nil {
		task.AssigneeName = strings.TrimSpace(*req.AssigneeName)
		if task.AssigneeName != "" {
			task.AssigneeID = nil
		}
	}
	if req.CounterpartID != nil {
		if *req.CounterpartID != 0 {
			var user model.User
			if err := h.db.First(&user, *req.CounterpartID).Error; err != nil {
				utils.Error(c, 400, "对口人不存在")
				return
			}
			task.CounterpartID = req.CounterpartID
		} else {
			task.CounterpartID = nil
		}
	}
	if req.CounterpartName != nil {
		task.CounterpartName = strings.TrimSpace(*req.CounterpartName)
		if task.CounterpartName != "" {
			task.CounterpartID = nil
		}
	}
	if req.ExternalContactID != nil {
		if *req.ExternalContactID == 0 {
			task.ExternalContactID = nil
		} else {
			var contact model.ExternalTaskContact
			if err := h.db.Where("id = ? AND project_id = ? AND enabled = ?", *req.ExternalContactID, task.ProjectID, true).First(&contact).Error; err != nil {
				utils.Error(c, 400, "乙方联系人不存在、已停用或不属于当前项目")
				return
			}
			task.ExternalContactID = req.ExternalContactID
			task.AssigneeName = contact.Name
			task.AssigneeID = nil
		}
	}
	if taskProject.ProjectType == "smart_warehouse" && task.NodeType == "task" && task.CounterpartID == nil {
		utils.Error(c, 400, "智慧仓储具体任务必须选择系统用户作为对口人")
		return
	}
	if req.StartDate != nil {
		if *req.StartDate != "" {
			if t, err := time.Parse("2006-01-02", *req.StartDate); err == nil {
				task.StartDate = &t
			}
		} else {
			task.StartDate = nil
		}
	}
	if req.EndDate != nil {
		if *req.EndDate != "" {
			if t, err := time.Parse("2006-01-02", *req.EndDate); err == nil {
				task.EndDate = &t
			}
		} else {
			task.EndDate = nil
		}
	}
	if req.DueDate != nil {
		if *req.DueDate != "" {
			if t, err := time.Parse("2006-01-02", *req.DueDate); err == nil {
				task.DueDate = &t
			}
		} else {
			task.DueDate = nil
		}
	}
	if req.Progress != nil {
		if *req.Progress < 0 || *req.Progress > 100 {
			utils.Error(c, 400, "进度值必须在0-100之间")
			return
		}
		task.Progress = *req.Progress
	}
	if req.PlanProgress != nil {
		if *req.PlanProgress < 0 || *req.PlanProgress > 100 {
			utils.Error(c, 400, "任务计划进度必须在0-100之间")
			return
		}
		task.PlanProgress = *req.PlanProgress
	}
	if req.TaskSequence != nil {
		task.TaskSequence = *req.TaskSequence
	}
	if req.Milestone1 != nil {
		task.Milestone1 = *req.Milestone1
	}
	if req.Milestone2 != nil {
		task.Milestone2 = *req.Milestone2
	}
	if req.Milestone3 != nil {
		task.Milestone3 = *req.Milestone3
	}
	if req.CurrentNode != nil {
		task.CurrentNode = *req.CurrentNode
	}
	if req.ReasonAnalysis != nil {
		task.ReasonAnalysis = *req.ReasonAnalysis
	}
	if req.RequiredSupport != nil {
		task.RequiredSupport = *req.RequiredSupport
	}
	if req.EstimatedHours != nil && taskProject.ProjectType != "smart_warehouse" {
		if *req.EstimatedHours < 0 {
			utils.Error(c, 400, "预估工时不能为负数")
			return
		}
		task.EstimatedHours = req.EstimatedHours
	}
	// 如果更新了实际工时，自动创建或更新资源分配
	if req.ActualHours != nil && taskProject.ProjectType != "smart_warehouse" {
		if *req.ActualHours < 0 {
			utils.Error(c, 400, "实际工时不能为负数")
			return
		}
		// 确定工作日期
		var workDate time.Time
		if req.WorkDate != nil && *req.WorkDate != "" {
			if t, err := time.Parse("2006-01-02", *req.WorkDate); err == nil {
				workDate = t
			} else {
				utils.Error(c, 400, "工作日期格式错误，应为 YYYY-MM-DD")
				return
			}
		} else {
			// 默认使用任务的开始日期或结束日期，如果都没有则使用今天
			if task.StartDate != nil {
				workDate = *task.StartDate
			} else if task.EndDate != nil {
				workDate = *task.EndDate
			} else {
				workDate = time.Now()
			}
		}
		workDate = time.Date(workDate.Year(), workDate.Month(), workDate.Day(), 0, 0, 0, 0, workDate.Location())

		// 同步到资源分配
		if err := h.syncTaskActualHours(&task, *req.ActualHours, workDate); err != nil {
			utils.Error(c, utils.CodeError, "同步资源分配失败: "+err.Error())
			return
		}
	}
	if taskProject.ProjectType == "smart_warehouse" {
		task.DueDate = nil
		task.Project = taskProject
		applySmartWarehouseCalculatedHours(&task, time.Now())
	}

	if err := h.db.Save(&task).Error; err != nil {
		utils.Error(c, utils.CodeError, "更新失败")
		return
	}

	// 计算并更新实际工时（从资源分配中汇总）
	if taskProject.ProjectType != "smart_warehouse" {
		h.calculateAndUpdateActualHours(&task)
		// 根据实际工时和预估工时自动计算进度
		h.calculateProgressFromHours(&task)
	}

	// 更新任务依赖关系
	if req.DependencyIDs != nil {
		var dependencies []model.Task
		if len(*req.DependencyIDs) > 0 {
			// 检查循环依赖
			for _, depID := range *req.DependencyIDs {
				if depID == task.ID {
					utils.Error(c, 400, "任务不能依赖自己")
					return
				}
			}
			if err := h.db.Where("id IN ?", *req.DependencyIDs).Find(&dependencies).Error; err != nil {
				utils.Error(c, 400, "依赖任务不存在")
				return
			}
		}
		if err := h.db.Model(&task).Association("Dependencies").Replace(dependencies); err != nil {
			utils.Error(c, utils.CodeError, "更新依赖失败")
			return
		}
	}

	// 重新加载关联数据
	h.db.Preload("Project").Preload("Parent").Preload("Parent.Parent").Preload("Children").Preload("Requirement").Preload("Creator").Preload("Assignee").Preload("Counterpart").Preload("Dependencies").First(&task, task.ID)
	applySmartWarehouseCalculatedHours(&task, time.Now())

	// 记录编辑操作和字段变更
	userID, exists := c.Get("user_id")
	if exists {
		dbValue, _ := c.Get("db")
		if db, ok := dbValue.(*gorm.DB); ok {
			// 比较新旧对象并记录变更
			utils.CompareAndRecord(db, oldTask, task, "task", task.ID, userID.(uint), "edited")
		}
	}

	utils.Success(c, task)
}

// DeleteTask 删除任务
func (h *TaskHandler) DeleteTask(c *gin.Context) {
	id := c.Param("id")

	// 验证任务是否存在
	var task model.Task
	if err := h.db.First(&task, id).Error; err != nil {
		utils.Error(c, 404, "任务不存在")
		return
	}

	// 权限检查：普通用户只能删除自己创建或参与的任务
	if !utils.CheckTaskAccess(h.db, c, task.ID) {
		utils.Error(c, 403, "没有权限删除该任务")
		return
	}
	if err := h.ensureProjectNotUnderReview(c, task.ProjectID); err != nil {
		utils.Error(c, 409, err.Error())
		return
	}

	// 检查是否有其他任务依赖此任务
	var count int64
	h.db.Model(&model.Task{}).Where("parent_id = ?", id).Count(&count)
	if count > 0 {
		utils.Error(c, 400, "任务下存在子任务，无法删除")
		return
	}
	h.db.Model(&model.TaskDependency{}).Where("dependency_id = ?", id).Count(&count)
	if count > 0 {
		utils.Error(c, 400, "有其他任务依赖此任务，无法删除")
		return
	}

	if err := h.db.Delete(&model.Task{}, id).Error; err != nil {
		utils.Error(c, utils.CodeError, "删除失败")
		return
	}

	utils.Success(c, gin.H{"message": "删除成功"})
}

// UpdateTaskStatus 更新任务状态
func (h *TaskHandler) UpdateTaskStatus(c *gin.Context) {
	id := c.Param("id")
	var task model.Task
	if err := h.db.First(&task, id).Error; err != nil {
		utils.Error(c, 404, "任务不存在")
		return
	}

	// 权限检查：普通用户只能更新自己创建或参与的任务
	if !utils.CheckTaskAccess(h.db, c, task.ID) {
		utils.Error(c, 403, "没有权限更新该任务")
		return
	}
	if err := h.ensureProjectNotUnderReview(c, task.ProjectID); err != nil {
		utils.Error(c, 409, err.Error())
		return
	}

	var req struct {
		Status string `json:"status" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		utils.Error(c, 400, "参数错误")
		return
	}

	// 验证状态
	validStatuses := map[string]bool{
		"wait":   true,
		"doing":  true,
		"done":   true,
		"pause":  true,
		"cancel": true,
		"closed": true,
	}
	if !validStatuses[req.Status] {
		utils.Error(c, 400, "状态值无效，有效值：wait, doing, done, pause, cancel, closed")
		return
	}

	oldStatus := task.Status
	task.Status = req.Status
	updateTaskCompletionTime(&task, oldStatus, time.Now())
	// 如果状态为done，自动设置进度为100
	if req.Status == "done" {
		task.Progress = 100
	}
	// 如果状态为cancel或closed，进度保持原值
	// 如果状态为doing且进度为0，可以设置一个默认值（可选）

	if err := h.db.Save(&task).Error; err != nil {
		utils.Error(c, utils.CodeError, "更新失败")
		return
	}

	// 重新加载关联数据
	h.db.Preload("Project").Preload("Parent").Preload("Parent.Parent").Preload("Children").Preload("Requirement").Preload("Creator").Preload("Assignee").Preload("Counterpart").Preload("Dependencies").First(&task, task.ID)
	applySmartWarehouseCalculatedHours(&task, time.Now())

	utils.Success(c, task)
}

// UpdateTaskProgress 更新任务进度
func (h *TaskHandler) UpdateTaskProgress(c *gin.Context) {
	id := c.Param("id")
	var task model.Task
	if err := h.db.First(&task, id).Error; err != nil {
		utils.Error(c, 404, "任务不存在")
		return
	}
	oldStatus := task.Status

	// 权限检查：普通用户只能更新自己创建或参与的任务
	if !utils.CheckTaskAccess(h.db, c, task.ID) {
		utils.Error(c, 403, "没有权限更新该任务")
		return
	}
	if err := h.ensureProjectNotUnderReview(c, task.ProjectID); err != nil {
		utils.Error(c, 409, err.Error())
		return
	}
	var taskProject model.Project
	if err := h.db.First(&taskProject, task.ProjectID).Error; err != nil {
		utils.Error(c, 400, "项目不存在")
		return
	}

	var req struct {
		Progress       *int     `json:"progress"`
		EstimatedHours *float64 `json:"estimated_hours"`
		ActualHours    *float64 `json:"actual_hours"` // 消耗工时
		WorkDate       *string  `json:"work_date"`    // 工作日期（YYYY-MM-DD）
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		utils.Error(c, 400, "参数错误")
		return
	}

	// 更新进度
	if req.Progress != nil {
		// 验证进度
		if *req.Progress < 0 || *req.Progress > 100 {
			utils.Error(c, 400, "进度值必须在0-100之间")
			return
		}
		task.Progress = *req.Progress
		// 如果进度为100，自动设置状态为done
		if *req.Progress == 100 {
			task.Status = "done"
		}
		// 如果进度大于0且状态为wait，自动设置为doing
		if *req.Progress > 0 && task.Status == "wait" {
			task.Status = "doing"
		}
	}
	updateTaskCompletionTime(&task, oldStatus, time.Now())

	// 更新预估工时
	if req.EstimatedHours != nil && taskProject.ProjectType != "smart_warehouse" {
		if *req.EstimatedHours < 0 {
			utils.Error(c, 400, "预估工时不能为负数")
			return
		}
		task.EstimatedHours = req.EstimatedHours
	}

	// 更新实际工时
	if req.ActualHours != nil && taskProject.ProjectType != "smart_warehouse" {
		if *req.ActualHours < 0 {
			utils.Error(c, 400, "实际工时不能为负数")
			return
		}
		// 确定工作日期
		var workDate time.Time
		if req.WorkDate != nil && *req.WorkDate != "" {
			if t, err := time.Parse("2006-01-02", *req.WorkDate); err == nil {
				workDate = t
			} else {
				utils.Error(c, 400, "工作日期格式错误，应为 YYYY-MM-DD")
				return
			}
		} else {
			// 默认使用任务的开始日期或结束日期，如果都没有则使用今天
			if task.StartDate != nil {
				workDate = *task.StartDate
			} else if task.EndDate != nil {
				workDate = *task.EndDate
			} else {
				workDate = time.Now()
			}
		}
		workDate = time.Date(workDate.Year(), workDate.Month(), workDate.Day(), 0, 0, 0, 0, workDate.Location())

		// 同步到资源分配
		if err := h.syncTaskActualHours(&task, *req.ActualHours, workDate); err != nil {
			utils.Error(c, utils.CodeError, "同步资源分配失败: "+err.Error())
			return
		}
	}

	if err := h.db.Save(&task).Error; err != nil {
		utils.Error(c, utils.CodeError, "更新失败")
		return
	}

	// 计算并更新实际工时（从资源分配中汇总）
	if taskProject.ProjectType != "smart_warehouse" {
		h.calculateAndUpdateActualHours(&task)
	}

	// 如果用户手动设置了进度，优先使用手动设置的进度，不根据工时自动计算
	// 只有在没有手动设置进度时，才根据工时自动计算进度
	if req.Progress == nil && taskProject.ProjectType != "smart_warehouse" {
		// 如果更新了实际工时或预估工时，自动根据工时计算进度
		// 进度 = 实际工时 / 预估工时 * 100，范围0-100%
		if req.ActualHours != nil || req.EstimatedHours != nil {
			h.calculateProgressFromHours(&task)
		} else {
			// 如果没有更新工时，根据当前工时计算进度
			h.calculateProgressFromHours(&task)
		}
	}
	// 如果 req.Progress != nil，说明用户手动设置了进度，已经在上面的代码中设置了，不需要再计算

	// 重新加载关联数据
	h.db.Preload("Project").Preload("Parent").Preload("Parent.Parent").Preload("Children").Preload("Requirement").Preload("Creator").Preload("Assignee").Preload("Counterpart").Preload("Dependencies").First(&task, task.ID)
	applySmartWarehouseCalculatedHours(&task, time.Now())

	utils.Success(c, task)
}

// syncTaskActualHours 同步任务实际工时到资源分配
func (h *TaskHandler) syncTaskActualHours(task *model.Task, actualHours float64, workDate time.Time) error {
	// 如果任务没有负责人，无法创建资源分配
	if task.AssigneeID == nil {
		return nil // 没有负责人时，不创建资源分配，但不报错
	}

	// 查找或创建资源
	var resource model.Resource
	err := h.db.Where("user_id = ? AND project_id = ?", *task.AssigneeID, task.ProjectID).First(&resource).Error
	if err != nil {
		// 资源不存在，创建资源
		resource = model.Resource{
			UserID:    *task.AssigneeID,
			ProjectID: task.ProjectID,
		}
		if err := h.db.Create(&resource).Error; err != nil {
			return err
		}
	}

	// 查找是否已存在该任务和日期的资源分配
	var allocation model.ResourceAllocation
	err = h.db.Where("resource_id = ? AND task_id = ? AND date = ?", resource.ID, task.ID, workDate).First(&allocation).Error
	if err != nil {
		// 不存在，创建新的资源分配
		allocation = model.ResourceAllocation{
			ResourceID:  resource.ID,
			TaskID:      &task.ID,
			ProjectID:   &task.ProjectID,
			Date:        workDate,
			Hours:       actualHours,
			Description: fmt.Sprintf("任务: %s", task.Title),
		}
		if err := h.db.Create(&allocation).Error; err != nil {
			return err
		}
	} else {
		// 存在，更新工时
		allocation.Hours = actualHours
		if err := h.db.Save(&allocation).Error; err != nil {
			return err
		}
	}

	return nil
}

// calculateAndUpdateActualHours 计算并更新任务的实际工时（从资源分配中汇总）
func (h *TaskHandler) calculateAndUpdateActualHours(task *model.Task) {
	var totalHours float64
	h.db.Model(&model.ResourceAllocation{}).
		Where("task_id = ?", task.ID).
		Select("COALESCE(SUM(hours), 0)").
		Scan(&totalHours)

	task.ActualHours = &totalHours
	h.db.Model(task).Update("actual_hours", totalHours)
}

// calculateProgressFromHours 根据实际工时和预估工时自动计算进度
func (h *TaskHandler) calculateProgressFromHours(task *model.Task) {
	// 如果预估工时未设置或为0，无法自动计算进度
	if task.EstimatedHours == nil || *task.EstimatedHours <= 0 {
		return
	}

	// 如果实际工时未设置，使用0
	actualHours := 0.0
	if task.ActualHours != nil {
		actualHours = *task.ActualHours
	}

	// 计算进度：实际工时 / 预估工时 * 100
	progress := int((actualHours / *task.EstimatedHours) * 100)

	// 进度不能超过100%
	if progress > 100 {
		progress = 100
	}

	// 如果进度小于0，设为0
	if progress < 0 {
		progress = 0
	}

	// 更新进度
	task.Progress = progress

	// 如果进度为100，自动设置状态为done
	if progress == 100 && task.Status != "done" {
		task.Status = "done"
	}
	// 如果进度大于0且状态为wait，自动设置为doing
	if progress > 0 && task.Status == "wait" {
		task.Status = "doing"
	}

	// 保存更新
	h.db.Model(task).Updates(map[string]interface{}{
		"progress": progress,
		"status":   task.Status,
	})
}

// GetTaskHistory 获取任务历史记录列表
func (h *TaskHandler) GetTaskHistory(c *gin.Context) {
	id := c.Param("id")
	var task model.Task
	if err := h.db.First(&task, id).Error; err != nil {
		utils.Error(c, 404, "任务不存在")
		return
	}

	// 权限检查：任务参与人和当前待审批人可以只读查看历史记录
	if !utils.CheckTaskReadAccess(h.db, c, task.ID) {
		utils.Error(c, 403, "没有权限查看该任务的历史记录")
		return
	}

	// 查询操作记录
	var actions []model.Action
	if err := h.db.Where("object_type = ? AND object_id = ?", "task", id).
		Preload("Actor").
		Preload("Histories").
		Order("date DESC").
		Find(&actions).Error; err != nil {
		utils.Error(c, utils.CodeError, "查询历史记录失败")
		return
	}

	// 处理历史记录，转换字段值显示
	for i := range actions {
		for j := range actions[i].Histories {
			processedHistory := utils.ProcessHistory(h.db, &actions[i].Histories[j])
			actions[i].Histories[j] = *processedHistory
		}
	}

	utils.Success(c, gin.H{
		"list": actions,
	})
}

// AddTaskHistoryNote 添加备注
func (h *TaskHandler) AddTaskHistoryNote(c *gin.Context) {
	id := c.Param("id")
	var task model.Task
	if err := h.db.First(&task, id).Error; err != nil {
		utils.Error(c, 404, "任务不存在")
		return
	}

	// 权限检查：普通用户只能为自己创建或参与的任务添加备注
	if !utils.CheckTaskAccess(h.db, c, task.ID) {
		utils.Error(c, 403, "没有权限为该任务添加备注")
		return
	}

	var req struct {
		Comment string `json:"comment" binding:"required"`
	}

	if err := c.ShouldBindJSON(&req); err != nil {
		utils.Error(c, 400, "参数错误")
		return
	}

	// 获取当前用户ID
	userID, exists := c.Get("user_id")
	if !exists {
		utils.Error(c, 401, "未登录")
		return
	}

	// 记录备注操作
	dbValue, _ := c.Get("db")
	if db, ok := dbValue.(*gorm.DB); ok {
		_, err := utils.RecordAction(db, "task", task.ID, "commented", userID.(uint), req.Comment, nil)
		if err != nil {
			utils.Error(c, utils.CodeError, "添加备注失败")
			return
		}
	}

	utils.Success(c, gin.H{"message": "添加备注成功"})
}
