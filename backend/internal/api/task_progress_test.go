package api

import (
	"testing"
	"time"

	"project-management/internal/model"
	"project-management/internal/utils"

	"gorm.io/driver/sqlite"
	"gorm.io/gorm"
)

func TestTaskDailyProgress(t *testing.T) {
	db, err := gorm.Open(sqlite.New(sqlite.Config{DriverName: "sqlite", DSN: ":memory:"}), &gorm.Config{})
	if err != nil {
		t.Fatal(err)
	}
	connection, err := db.DB()
	if err != nil {
		t.Fatal(err)
	}
	connection.SetMaxOpenConns(1)
	t.Cleanup(func() { connection.Close() })
	if err := db.AutoMigrate(&model.Action{}, &model.History{}); err != nil {
		t.Fatal(err)
	}
	created := time.Date(2026, 10, 16, 8, 0, 0, 0, time.UTC)
	old := model.Task{ID: 1, CreatedAt: created, Description: "10月20号发出"}
	updated := old
	updated.Description = "10月20号发出\n10月20号到货"
	if _, err := utils.CompareAndRecord(db, old, updated, "task", 1, 1, "edited"); err != nil {
		t.Fatal(err)
	}
	cleared := updated
	cleared.Description = ""
	if _, err := utils.CompareAndRecord(db, updated, cleared, "task", 1, 1, "edited"); err != nil {
		t.Fatal(err)
	}
	// 重复保存及其他字段变化不能产生额外的描述版本。
	if _, err := utils.CompareAndRecord(db, cleared, cleared, "task", 1, 1, "edited"); err != nil {
		t.Fatal(err)
	}
	renamed := cleared
	renamed.Title = "新标题"
	if _, err := utils.CompareAndRecord(db, cleared, renamed, "task", 1, 1, "edited"); err != nil {
		t.Fatal(err)
	}
	if _, err := utils.CompareAndRecord(db, old, updated, "bug", 1, 1, "edited"); err != nil {
		t.Fatal(err)
	}
	tasks := []model.Task{cleared, {ID: 2, Description: "已有描述", UpdatedAt: created}, {ID: 3}}
	if err := NewTaskHandler(db).loadTaskDailyProgress(tasks); err != nil {
		t.Fatal(err)
	}
	entries := tasks[0].DailyProgress
	if len(entries) != 3 || entries[0].Content != "" || entries[1].Content != updated.Description || entries[2].Content != old.Description {
		t.Fatalf("unexpected revisions: %+v", entries)
	}
	if len(tasks[1].DailyProgress) != 1 || tasks[1].DailyProgress[0].Content != "已有描述" {
		t.Fatal("existing description missing")
	}
	if len(tasks[2].DailyProgress) != 0 {
		t.Fatal("unexpected progress for empty task")
	}
	if err := NewTaskHandler(db).loadTaskDailyProgress(nil); err != nil {
		t.Fatal(err)
	}
}
