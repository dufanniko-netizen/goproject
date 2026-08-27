package model

import (
	"time"

	"gorm.io/gorm"
)

// Project 项目表
type Project struct {
	ID        uint           `gorm:"primarykey" json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`

	Name            string            `gorm:"size:100;not null" json:"name"`                                        // 项目名称
	Code            string            `gorm:"size:50;uniqueIndex" json:"code"`                                      // 项目编码
	Description     string            `gorm:"type:text" json:"description"`                                         // 描述
	Status          string            `gorm:"size:20;default:'wait'" json:"status"`                                 // 状态：wait(未开始), doing(进行中), suspended(已挂起), closed(已关闭), done(已完成)
	StartDate       *time.Time        `json:"start_date"`                                                           // 开始日期
	EndDate         *time.Time        `json:"end_date"`                                                             // 结束日期
	ProjectType     string            `gorm:"size:30;default:'smart_warehouse';not null;index" json:"project_type"` // smart_warehouse, automation
	ApprovalStatus  string            `gorm:"size:20;default:'published';not null;index" json:"approval_status"`    // draft, pending, rejected, published
	CreatorID       *uint             `gorm:"index" json:"creator_id"`
	Creator         *User             `gorm:"foreignKey:CreatorID" json:"creator,omitempty"`
	SubmittedAt     *time.Time        `json:"submitted_at"`
	PublishedAt     *time.Time        `json:"published_at"`
	ApprovalRecords []ProjectApproval `gorm:"foreignKey:ProjectID" json:"approval_records,omitempty"`

	Members      []ProjectMember `gorm:"foreignKey:ProjectID" json:"members,omitempty"`
	Tasks        []Task          `gorm:"foreignKey:ProjectID" json:"tasks,omitempty"`
	Bugs         []Bug           `gorm:"foreignKey:ProjectID" json:"bugs,omitempty"`
	Requirements []Requirement   `gorm:"foreignKey:ProjectID" json:"requirements,omitempty"`
	TestCases    []TestCase      `gorm:"foreignKey:ProjectID" json:"test_cases,omitempty"`
	Boards       []Board         `gorm:"foreignKey:ProjectID" json:"boards,omitempty"`
	Tags         []Tag           `gorm:"many2many:project_tags;" json:"tags,omitempty"` // 标签（多对多关联）
}

// ProjectApproval 项目审批记录。每次重新提交都会新增一条，完整保留驳回意见。
type ProjectApproval struct {
	ID          uint       `gorm:"primarykey" json:"id"`
	CreatedAt   time.Time  `json:"created_at"`
	UpdatedAt   time.Time  `json:"updated_at"`
	ProjectID   uint       `gorm:"index;not null" json:"project_id"`
	Project     Project    `gorm:"foreignKey:ProjectID" json:"project,omitempty"`
	SubmitterID uint       `gorm:"index;not null" json:"submitter_id"`
	Submitter   User       `gorm:"foreignKey:SubmitterID" json:"submitter,omitempty"`
	ReviewerID  *uint      `gorm:"index" json:"reviewer_id"`
	Reviewer    *User      `gorm:"foreignKey:ReviewerID" json:"reviewer,omitempty"`
	Status      string     `gorm:"size:20;default:'pending';not null;index" json:"status"`
	Comment     string     `gorm:"type:text" json:"comment"`
	SubmittedAt time.Time  `json:"submitted_at"`
	ReviewedAt  *time.Time `json:"reviewed_at"`
}

// ProjectMember 项目成员表
type ProjectMember struct {
	ID        uint           `gorm:"primarykey" json:"id"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
	DeletedAt gorm.DeletedAt `gorm:"index" json:"-"`

	ProjectID uint    `gorm:"index" json:"project_id"`
	Project   Project `gorm:"foreignKey:ProjectID" json:"project,omitempty"`

	UserID uint `gorm:"index" json:"user_id"`
	User   User `gorm:"foreignKey:UserID" json:"user,omitempty"`

	Role string `gorm:"size:50" json:"role"` // 项目角色：owner, member, viewer
}
