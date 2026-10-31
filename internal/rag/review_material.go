package rag

import (
	"context"
	"fmt"
	"strings"

	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	p "github.com/KanaDoodle/CampusTrace/internal/persistence"
)

// Origin is assigned by the review import endpoint, never by generic text
// imports. It records a source snapshot; edited study notes remain references,
// not evidence of skills or an assertion that the review is still unchanged.
type DocumentOrigin struct {
	Kind          string `json:"kind"`
	ApplicationID string `json:"application_id"`
	InterviewID   string `json:"interview_id"`
	ReviewID      string `json:"review_id"`
	SourceKey     string `json:"source_key"`
}

type ReviewMaterialDraft struct {
	Title  string         `json:"title"`
	Text   string         `json:"text"`
	Origin DocumentOrigin `json:"origin"`
}

type reviewReference struct {
	InterviewID string
	Key         string
}

func reviewOrigin(user string, i d.Interview, r d.Review) DocumentOrigin {
	return DocumentOrigin{Kind: "interview_review", ApplicationID: i.ApplicationID, InterviewID: i.ID, ReviewID: r.ID, SourceKey: d.Hash(d.JSON([]any{"review-material-v1", user, i.ID, i.ApplicationID, r}))}
}

func reviewSource(ctx context.Context, q p.Queryer, user, id string) (d.Interview, d.Review, error) {
	v, err := p.One[struct {
		Interview d.Interview `json:"interview"`
		Review    d.Review    `json:"review"`
	}](ctx, q, `SELECT JSON_OBJECT('interview',i.body,'review',r.body)
 FROM interviews i JOIN applications a ON a.id=i.application_id AND a.user_id=i.user_id
 JOIN reviews r ON r.interview_id=i.id AND r.user_id=i.user_id
 WHERE i.id=? AND i.user_id=?`, id, user)
	return v.Interview, v.Review, err
}

func (s *Service) ReviewMaterialDraft(ctx context.Context, user, id string) (ReviewMaterialDraft, error) {
	record, err := s.Store.InterviewRecord(ctx, user, id)
	if err != nil {
		return ReviewMaterialDraft{}, err
	}
	if record.Review == nil {
		return ReviewMaterialDraft{}, p.ErrNotFound
	}
	company := "面试"
	if record.Job != nil {
		runes := []rune(record.Job.Company)
		company = string(runes[:min(len(runes), 40)])
	}
	title := fmt.Sprintf("%s · 第 %d 轮复盘笔记", company, record.Round)
	return ReviewMaterialDraft{Title: title, Text: reviewMaterialText(*record.Review), Origin: reviewOrigin(user, record.Interview, *record.Review)}, nil
}

func reviewMaterialText(review d.Review) string {
	var text strings.Builder
	text.WriteString("面试复盘学习笔记（可编辑，不代表个人能力证明）\n\n实际被问到的问题\n")
	for _, question := range review.Questions {
		text.WriteString("- " + question + "\n")
	}
	text.WriteString("\n自我复盘\n" + review.Evaluation + "\n\n未答好的要点\n")
	for _, missed := range review.Missed {
		text.WriteString("- " + missed + "\n")
	}
	if review.FollowUp != "" {
		text.WriteString("\n后续复习计划\n" + review.FollowUp + "\n")
	}
	return text.String()
}

// Validation, origin binding, deduplication, document/chunk creation and corpus
// revision fencing share the same user lock as review writes and deletions.
func (s *Service) ImportReviewMaterial(ctx context.Context, user, id, key, title, text string) (Document, bool, error) {
	if len(id) != 32 || len(key) != 64 {
		return Document{}, false, p.ErrValidation
	}
	return s.ingest(ctx, user, Document{Title: title, Text: text}, &reviewReference{InterviewID: id, Key: key})
}
