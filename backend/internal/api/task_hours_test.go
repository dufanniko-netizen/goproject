package api

import (
	"testing"
	"time"

	"project-management/internal/model"
)

func TestApplySmartWarehouseCalculatedHours(t *testing.T) {
	start := time.Date(2026, 8, 1, 0, 0, 0, 0, time.UTC)
	end := time.Date(2026, 8, 3, 0, 0, 0, 0, time.UTC)
	task := model.Task{Project: model.Project{ProjectType: "smart_warehouse"}, StartDate: &start, EndDate: &end}

	applySmartWarehouseCalculatedHours(&task, time.Date(2026, 8, 2, 16, 0, 0, 0, time.UTC))

	if task.PlannedDays != 3 || task.EstimatedHours == nil || *task.EstimatedHours != 24 {
		t.Fatalf("planned calculation = %d days/%v hours", task.PlannedDays, task.EstimatedHours)
	}
	if task.ActualDays != 2 || task.ActualHours == nil || *task.ActualHours != 16 {
		t.Fatalf("actual calculation = %d days/%v hours", task.ActualDays, task.ActualHours)
	}
}

func TestApplySmartWarehouseCalculatedHoursBeforeStart(t *testing.T) {
	start := time.Date(2026, 8, 10, 0, 0, 0, 0, time.UTC)
	task := model.Task{Project: model.Project{ProjectType: "smart_warehouse"}, StartDate: &start}
	applySmartWarehouseCalculatedHours(&task, time.Date(2026, 8, 2, 0, 0, 0, 0, time.UTC))
	if task.ActualDays != 0 || task.ActualHours == nil || *task.ActualHours != 0 {
		t.Fatalf("future task actual calculation = %d days/%v hours", task.ActualDays, task.ActualHours)
	}
}
