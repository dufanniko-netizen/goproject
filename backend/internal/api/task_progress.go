package api

import (
	"time"

	"project-management/internal/model"
)

// loadTaskDailyProgress 批量读取描述历史，避免为每一行单独查询。
func (h *TaskHandler) loadTaskDailyProgress(tasks []model.Task) error {
	if len(tasks) == 0 {
		return nil
	}
	ids := make([]uint, len(tasks))
	for i := range tasks {
		ids[i] = tasks[i].ID
		tasks[i].DailyProgress = []model.TaskProgressEntry{}
	}
	var revisions []struct {
		ObjectID uint
		Date     time.Time
		Old      string
		New      string
	}
	if err := h.db.Table("histories").
		Select("actions.object_id, actions.date, histories.old, histories.new").
		Joins("JOIN actions ON actions.id = histories.action_id").
		Where("actions.object_type = ? AND actions.object_id IN ? AND histories.field = ?", "task", ids, "description").
		Order("actions.date ASC, actions.id ASC, histories.id ASC").Scan(&revisions).Error; err != nil {
		return err
	}
	byID := make(map[uint]*model.Task, len(tasks))
	for i := range tasks {
		byID[tasks[i].ID] = &tasks[i]
	}
	for _, revision := range revisions {
		task := byID[revision.ObjectID]
		if len(task.DailyProgress) == 0 && revision.Old != "" {
			task.DailyProgress = append(task.DailyProgress, model.TaskProgressEntry{Date: task.CreatedAt, Content: revision.Old})
		}
		task.DailyProgress = append(task.DailyProgress, model.TaskProgressEntry{Date: revision.Date, Content: revision.New})
	}
	for i := range tasks {
		task := &tasks[i]
		if len(task.DailyProgress) == 0 && task.Description != "" {
			task.DailyProgress = append(task.DailyProgress, model.TaskProgressEntry{Date: task.UpdatedAt, Content: task.Description})
		}
		// 最近的更新优先展示；同一天的多次修改也逐条保留。
		for left, right := 0, len(task.DailyProgress)-1; left < right; left, right = left+1, right-1 {
			task.DailyProgress[left], task.DailyProgress[right] = task.DailyProgress[right], task.DailyProgress[left]
		}
	}
	return nil
}
