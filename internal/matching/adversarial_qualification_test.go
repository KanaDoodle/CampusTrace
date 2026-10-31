package matching

import (
	d "github.com/KanaDoodle/CampusTrace/internal/domain"
	"testing"
	"time"
)

func TestAdversarialQualificationCannotInferMissingDegree(t *testing.T) {
	reqs := []Requirement{{ID: "doctoral-gate", Category: "QUALIFICATION", ClaimType: "EDUCATION_REQUIREMENT", Value: "PHD", Confidence: 1, Text: "博士学历", Excerpt: "学历要求：博士学历"}}
	job := d.Job{ID: "synthetic", JobType: "FULL_TIME"}
	blocked := Qualification(job, d.Profile{Degree: "BACHELOR"}, reqs, time.Now())
	missing := Qualification(job, d.Profile{}, reqs, time.Now())
	if blocked.Status != "INELIGIBLE" || missing.Status != "UNKNOWN" {
		t.Fatal("hard gate or missing information misclassified", blocked, missing)
	}
}
