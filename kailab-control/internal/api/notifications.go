package api

import (
	"encoding/json"
	"log"
	"net/http"
)

// NotifyCommentRequest is the request body for comment notifications.
type NotifyCommentRequest struct {
	// Org is the organization slug
	Org string `json:"org"`
	// Repo is the repository name
	Repo string `json:"repo"`
	// ReviewID is the review identifier
	ReviewID string `json:"reviewId"`
	// ReviewTitle is the review title for the email
	ReviewTitle string `json:"reviewTitle"`
	// ReviewAuthor is the email/username of the review author
	ReviewAuthor string `json:"reviewAuthor"`
	// CommentAuthor is the email/username of who wrote the comment
	CommentAuthor string `json:"commentAuthor"`
	// CommentBody is the comment text
	CommentBody string `json:"commentBody"`
	// ParentCommentAuthor is set if this is a reply (the author of the parent comment)
	ParentCommentAuthor string `json:"parentCommentAuthor,omitempty"`
}

// NotifyComment handles internal notifications for new comments.
// Called by kailab data plane when a comment is created.
func (h *Handler) NotifyComment(w http.ResponseWriter, r *http.Request) {
	if h.email == nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "skipped", "reason": "email not configured"})
		return
	}

	var req NotifyCommentRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	// Build review URL
	reviewURL := h.cfg.BaseURL + "/orgs/" + req.Org + "/" + req.Repo + "/reviews/" + req.ReviewID

	// Determine who to notify
	var notifyEmail string
	var isReply bool

	if req.ParentCommentAuthor != "" {
		// This is a reply - notify the parent comment author
		notifyEmail = req.ParentCommentAuthor
		isReply = true
	} else {
		// New comment on review - notify review author
		notifyEmail = req.ReviewAuthor
		isReply = false
	}

	// Don't notify yourself
	if notifyEmail == req.CommentAuthor {
		writeJSON(w, http.StatusOK, map[string]string{"status": "skipped", "reason": "self comment"})
		return
	}

	// Look up user email if notifyEmail is a user ID
	user, err := h.db.GetUserByID(notifyEmail)
	if err != nil {
		// Try as email directly
		user, err = h.db.GetUserByEmail(notifyEmail)
	}
	if err != nil || user == nil {
		log.Printf("notify: could not find user %s: %v", notifyEmail, err)
		writeJSON(w, http.StatusOK, map[string]string{"status": "skipped", "reason": "user not found"})
		return
	}

	// Get commenter name for the email
	commenterName := req.CommentAuthor
	commenter, _ := h.db.GetUserByEmail(req.CommentAuthor)
	if commenter == nil {
		commenter, _ = h.db.GetUserByID(req.CommentAuthor)
	}
	if commenter != nil && commenter.Name != "" {
		commenterName = commenter.Name
	}

	// Use review title or fallback
	reviewTitle := req.ReviewTitle
	if reviewTitle == "" {
		reviewTitle = "Review " + req.ReviewID
	}

	// Send the notification
	err = h.email.SendCommentNotification(
		user.Email,
		commenterName,
		reviewTitle,
		req.CommentBody,
		reviewURL,
		isReply,
	)
	if err != nil {
		log.Printf("notify: failed to send email to %s: %v", user.Email, err)
		writeError(w, http.StatusInternalServerError, "failed to send notification", err)
		return
	}

	log.Printf("notify: sent comment notification to %s for review %s/%s/%s", user.Email, req.Org, req.Repo, req.ReviewID)
	writeJSON(w, http.StatusOK, map[string]string{"status": "sent", "to": user.Email})
}
