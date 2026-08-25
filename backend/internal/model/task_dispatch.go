package model

import (
	"time"

	"gorm.io/gorm"
)

// ExternalTaskContact 是无需系统账号的乙方任务收件联系人。
type ExternalTaskContact struct {
	ID        uint           `gorm:"primarykey" json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`
	ProjectID uint           `gorm:"not null;uniqueIndex:idx_contact_project_name" json:"project_id"`
	Name      string         `gorm:"size:100;not null;uniqueIndex:idx_contact_project_name" json:"name"`
	Company   string         `gorm:"size:150" json:"company"`
	Recipient string         `gorm:"size:100" json:"recipient"`
	Email     string         `gorm:"size:200;not null" json:"email"`
	CCEmails  string         `gorm:"type:text" json:"cc_emails"`
	Enabled   bool           `gorm:"default:true;index" json:"enabled"`
	Notes     string         `gorm:"type:text" json:"notes"`
}

type TaskDispatchBatch struct {
	ID            uint                `gorm:"primarykey" json:"id"`
	CreatedAt     time.Time           `json:"created_at"`
	UpdatedAt     time.Time           `json:"updated_at"`
	DeletedAt     gorm.DeletedAt      `gorm:"index" json:"-"`
	Token         string              `gorm:"size:64;uniqueIndex;not null" json:"token"`
	ProjectID     uint                `gorm:"index;not null" json:"project_id"`
	ContactID     uint                `gorm:"index;not null" json:"contact_id"`
	Contact       ExternalTaskContact `gorm:"foreignKey:ContactID" json:"contact,omitempty"`
	Status        string              `gorm:"size:30;index;default:'pending'" json:"status"`
	TaskCount     int                 `json:"task_count"`
	FilePath      string              `gorm:"size:500" json:"file_path"`
	SentAt        *time.Time          `json:"sent_at"`
	ReceivedAt    *time.Time          `json:"received_at"`
	DispatchDate  string              `gorm:"size:10;index" json:"dispatch_date"`
	SourceMessage string              `gorm:"size:255;index" json:"source_message"`
	SuccessCount  int                 `json:"success_count"`
	FailureCount  int                 `json:"failure_count"`
	ErrorMessage  string              `gorm:"type:text" json:"error_message"`
}

type TaskDispatchItem struct {
	ID        uint      `gorm:"primarykey" json:"id"`
	CreatedAt time.Time `json:"created_at"`
	BatchID   uint      `gorm:"index;not null;uniqueIndex:idx_batch_task" json:"batch_id"`
	TaskID    uint      `gorm:"index;not null;uniqueIndex:idx_batch_task" json:"task_id"`
}

type TaskTimeChangeRequest struct {
	ID             uint           `gorm:"primarykey" json:"id"`
	CreatedAt      time.Time      `json:"created_at"`
	UpdatedAt      time.Time      `json:"updated_at"`
	DeletedAt      gorm.DeletedAt `gorm:"index" json:"-"`
	TaskID         uint           `gorm:"index;not null" json:"task_id"`
	Task           Task           `gorm:"foreignKey:TaskID" json:"task,omitempty"`
	BatchID        uint           `gorm:"index;not null" json:"batch_id"`
	RequestedStart *time.Time     `json:"requested_start"`
	RequestedEnd   *time.Time     `json:"requested_end"`
	Reason         string         `gorm:"type:text" json:"reason"`
	Status         string         `gorm:"size:20;index;default:'pending'" json:"status"`
	ReviewerID     uint           `gorm:"index;not null" json:"reviewer_id"`
	Reviewer       User           `gorm:"foreignKey:ReviewerID" json:"reviewer,omitempty"`
	ReviewedAt     *time.Time     `json:"reviewed_at"`
	ReviewComment  string         `gorm:"type:text" json:"review_comment"`
}
