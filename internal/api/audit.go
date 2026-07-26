package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/foxc888/foxos/internal/domain"
)

type AuditReader interface{AuditEvents(context.Context,int)([]domain.AuditEvent,error)}

type auditOutput struct{
	ID string `json:"id"`
	Action string `json:"action"`
	TargetID string `json:"targetId"`
	Outcome domain.AuditOutcome `json:"outcome"`
	Details map[string]any `json:"details,omitempty"`
	CreatedAt string `json:"createdAt"`
	UpdatedAt string `json:"updatedAt"`
}
func(s *Server)RegisterAudit(mux *http.ServeMux,reader AuditReader){
	mux.Handle("GET /api/v1/audit-events",s.auth(http.HandlerFunc(func(w http.ResponseWriter,r *http.Request){
		limit:=100;if value:=r.URL.Query().Get("limit");value!=""{parsed,err:=strconv.Atoi(value);if err!=nil||parsed<1||parsed>500{problem(w,400,"invalid_limit",errors.New("limit must be between 1 and 500"));return};limit=parsed}
		events,err:=reader.AuditEvents(r.Context(),limit);if err!=nil{problem(w,500,"audit_read_failed",err);return}
		out:=make([]auditOutput,0,len(events));for _,event:=range events{out=append(out,auditOutput{ID:event.ID,Action:event.Action,TargetID:event.TargetID,Outcome:event.Outcome,Details:event.Details,CreatedAt:event.CreatedAt.Format(time.RFC3339Nano),UpdatedAt:event.UpdatedAt.Format(time.RFC3339Nano)})}
		writeJSON(w,200,out)
	})))
}
