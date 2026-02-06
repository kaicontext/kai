package api

import (
	"encoding/json"
	"log"
	"net/http"

	"kailab-control/internal/email"
)

// NotifyReviewRequest is the request body for review created notifications.
type NotifyReviewRequest struct {
	// Org is the organization slug
	Org string `json:"org"`
	// Repo is the repository name
	Repo string `json:"repo"`
	// ReviewID is the review identifier
	ReviewID string `json:"reviewId"`
	// ReviewTitle is the review title
	ReviewTitle string `json:"reviewTitle"`
	// ReviewAuthor is the email/username of the review author
	ReviewAuthor string `json:"reviewAuthor"`
	// Reviewers is the list of reviewers to notify
	Reviewers []string `json:"reviewers"`
}

// NotifyReview handles internal notifications for new reviews.
// Called by kailab data plane when a review is created.
func (h *Handler) NotifyReview(w http.ResponseWriter, r *http.Request) {
	if h.email == nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "skipped", "reason": "email not configured"})
		return
	}

	var req NotifyReviewRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	// Build review URL
	reviewURL := h.cfg.BaseURL + "/orgs/" + req.Org + "/" + req.Repo + "/reviews/" + req.ReviewID

	// Get author name for emails
	authorName := req.ReviewAuthor
	author, _ := h.db.GetUserByEmail(req.ReviewAuthor)
	if author == nil {
		author, _ = h.db.GetUserByID(req.ReviewAuthor)
	}
	if author != nil && author.Name != "" {
		authorName = author.Name
	}

	// Use review title or fallback
	reviewTitle := req.ReviewTitle
	if reviewTitle == "" {
		reviewTitle = "Review " + req.ReviewID
	}

	// Track who we've notified to avoid duplicates
	notified := make(map[string]bool)
	notified[req.ReviewAuthor] = true // Don't notify the author

	var sentTo []string

	// Notify all reviewers
	for _, reviewer := range req.Reviewers {
		if notified[reviewer] {
			continue
		}

		user, err := h.db.GetUserByEmail(reviewer)
		if err != nil {
			user, err = h.db.GetUserByID(reviewer)
		}
		if err != nil || user == nil {
			continue
		}

		if notified[user.Email] {
			continue
		}

		err = h.email.SendReviewCreated(
			user.Email,
			authorName,
			reviewTitle,
			reviewURL,
			req.Org,
			req.Repo,
		)
		if err != nil {
			log.Printf("notify: failed to send review email to %s: %v", user.Email, err)
		} else {
			sentTo = append(sentTo, user.Email)
			notified[reviewer] = true
			notified[user.Email] = true
		}
	}

	if len(sentTo) == 0 {
		writeJSON(w, http.StatusOK, map[string]string{"status": "skipped", "reason": "no recipients"})
		return
	}

	log.Printf("notify: sent review notifications to %v for %s/%s/%s", sentTo, req.Org, req.Repo, req.ReviewID)
	writeJSON(w, http.StatusOK, map[string]interface{}{"status": "sent", "to": sentTo})
}

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
	// Mentions is a list of @mentioned usernames
	Mentions []string `json:"mentions,omitempty"`
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

	// Get commenter name for emails
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

	// Track who we've notified to avoid duplicates
	notified := make(map[string]bool)
	notified[req.CommentAuthor] = true // Don't notify the commenter

	var sentTo []string

	// Notify reply recipient or review author
	var primaryNotify string
	var isReply bool
	if req.ParentCommentAuthor != "" {
		primaryNotify = req.ParentCommentAuthor
		isReply = true
	} else {
		primaryNotify = req.ReviewAuthor
		isReply = false
	}

	if primaryNotify != "" && !notified[primaryNotify] {
		user, err := h.db.GetUserByEmail(primaryNotify)
		if err != nil {
			user, err = h.db.GetUserByID(primaryNotify)
		}
		if err == nil && user != nil {
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
			} else {
				sentTo = append(sentTo, user.Email)
				notified[primaryNotify] = true
				notified[user.Email] = true
			}
		}
	}

	// Notify @mentioned users
	for _, mention := range req.Mentions {
		if notified[mention] {
			continue
		}

		user, err := h.db.GetUserByEmail(mention)
		if err != nil {
			user, err = h.db.GetUserByID(mention)
		}
		if err != nil || user == nil {
			// Try treating mention as a partial email match
			continue
		}

		if notified[user.Email] {
			continue
		}

		err = h.email.SendMentionNotification(
			user.Email,
			commenterName,
			reviewTitle,
			req.CommentBody,
			reviewURL,
		)
		if err != nil {
			log.Printf("notify: failed to send mention email to %s: %v", user.Email, err)
		} else {
			sentTo = append(sentTo, user.Email)
			notified[mention] = true
			notified[user.Email] = true
		}
	}

	if len(sentTo) == 0 {
		writeJSON(w, http.StatusOK, map[string]string{"status": "skipped", "reason": "no recipients"})
		return
	}

	log.Printf("notify: sent notifications to %v for review %s/%s/%s", sentTo, req.Org, req.Repo, req.ReviewID)
	writeJSON(w, http.StatusOK, map[string]interface{}{"status": "sent", "to": sentTo})
}

// NotifyPipelineRequest is the request body for pipeline completion notifications.
type NotifyPipelineRequest struct {
	Org          string `json:"org"`
	Repo         string `json:"repo"`
	WorkflowName string `json:"workflowName"`
	RunID        string `json:"runId"`
	Conclusion   string `json:"conclusion"` // success or failure
	TriggerRef   string `json:"triggerRef"`
	TriggerSHA   string `json:"triggerSha"`
	Author       string `json:"author"` // Who triggered the run
}

// NotifyPipeline handles notifications for CI pipeline completion.
func (h *Handler) NotifyPipeline(w http.ResponseWriter, r *http.Request) {
	if h.email == nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "skipped", "reason": "email not configured"})
		return
	}

	var req NotifyPipelineRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	// Build run URL
	runURL := h.cfg.BaseURL + "/orgs/" + req.Org + "/" + req.Repo + "/workflows/runs/" + req.RunID

	// Notify the author
	if req.Author == "" {
		writeJSON(w, http.StatusOK, map[string]string{"status": "skipped", "reason": "no author"})
		return
	}

	user, err := h.db.GetUserByEmail(req.Author)
	if err != nil {
		user, err = h.db.GetUserByID(req.Author)
	}
	if err != nil || user == nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "skipped", "reason": "user not found"})
		return
	}

	err = h.email.SendPipelineResult(
		user.Email,
		req.Org,
		req.Repo,
		req.WorkflowName,
		req.Conclusion,
		runURL,
		req.TriggerRef,
		req.TriggerSHA,
	)
	if err != nil {
		log.Printf("notify: failed to send pipeline email to %s: %v", user.Email, err)
		writeError(w, http.StatusInternalServerError, "failed to send email", err)
		return
	}

	log.Printf("notify: sent pipeline %s notification to %s for %s/%s", req.Conclusion, user.Email, req.Org, req.Repo)
	writeJSON(w, http.StatusOK, map[string]interface{}{"status": "sent", "to": user.Email})
}

// NotifyRequestChangesRequest is the request body for request changes notifications.
type NotifyRequestChangesRequest struct {
	Org          string `json:"org"`
	Repo         string `json:"repo"`
	ReviewID     string `json:"reviewId"`
	ReviewTitle  string `json:"reviewTitle"`
	ReviewAuthor string `json:"reviewAuthor"`
	ReviewerName string `json:"reviewerName"`
	Comments     []struct {
		FilePath string `json:"filePath"`
		Line     int    `json:"line"`
		Body     string `json:"body"`
	} `json:"comments"`
}

// NotifyRequestChanges handles notifications when a reviewer requests changes.
func (h *Handler) NotifyRequestChanges(w http.ResponseWriter, r *http.Request) {
	if h.email == nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "skipped", "reason": "email not configured"})
		return
	}

	var req NotifyRequestChangesRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	reviewURL := h.cfg.BaseURL + "/orgs/" + req.Org + "/" + req.Repo + "/reviews/" + req.ReviewID

	// Notify the review author
	user, err := h.db.GetUserByEmail(req.ReviewAuthor)
	if err != nil {
		user, err = h.db.GetUserByID(req.ReviewAuthor)
	}
	if err != nil || user == nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "skipped", "reason": "author not found"})
		return
	}

	// Convert comments
	comments := make([]email.Comment, len(req.Comments))
	for i, c := range req.Comments {
		comments[i] = email.Comment{
			FilePath: c.FilePath,
			Line:     c.Line,
			Body:     c.Body,
		}
	}

	err = h.email.SendRequestChanges(
		user.Email,
		req.ReviewerName,
		req.ReviewTitle,
		reviewURL,
		req.Org,
		req.Repo,
		comments,
	)
	if err != nil {
		log.Printf("notify: failed to send request changes email to %s: %v", user.Email, err)
		writeError(w, http.StatusInternalServerError, "failed to send email", err)
		return
	}

	log.Printf("notify: sent request changes notification to %s for %s/%s/%s", user.Email, req.Org, req.Repo, req.ReviewID)
	writeJSON(w, http.StatusOK, map[string]interface{}{"status": "sent", "to": user.Email})
}

// NotifyReviewStateRequest is the request body for review state change notifications.
type NotifyReviewStateRequest struct {
	Org          string   `json:"org"`
	Repo         string   `json:"repo"`
	ReviewID     string   `json:"reviewId"`
	ReviewTitle  string   `json:"reviewTitle"`
	ReviewAuthor string   `json:"reviewAuthor"`
	ActorName    string   `json:"actorName"`
	State        string   `json:"state"` // merged, abandoned, approved, ready
	TargetBranch string   `json:"targetBranch,omitempty"`
	Reason       string   `json:"reason,omitempty"`
	Reviewers    []string `json:"reviewers,omitempty"`
}

// NotifyReviewState handles notifications for review state changes.
func (h *Handler) NotifyReviewState(w http.ResponseWriter, r *http.Request) {
	if h.email == nil {
		writeJSON(w, http.StatusOK, map[string]string{"status": "skipped", "reason": "email not configured"})
		return
	}

	var req NotifyReviewStateRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeError(w, http.StatusBadRequest, "invalid request body", err)
		return
	}

	reviewURL := h.cfg.BaseURL + "/orgs/" + req.Org + "/" + req.Repo + "/reviews/" + req.ReviewID

	// Determine who to notify based on state
	var recipients []string
	switch req.State {
	case "merged", "abandoned":
		// Notify the review author and all reviewers
		recipients = append(recipients, req.ReviewAuthor)
		recipients = append(recipients, req.Reviewers...)
	case "approved":
		// Notify the review author
		recipients = append(recipients, req.ReviewAuthor)
	case "ready":
		// Notify all reviewers
		recipients = req.Reviewers
	default:
		writeJSON(w, http.StatusOK, map[string]string{"status": "skipped", "reason": "unknown state"})
		return
	}

	notified := make(map[string]bool)
	notified[req.ActorName] = true // Don't notify the actor
	var sentTo []string

	for _, recipient := range recipients {
		if notified[recipient] {
			continue
		}

		user, err := h.db.GetUserByEmail(recipient)
		if err != nil {
			user, err = h.db.GetUserByID(recipient)
		}
		if err != nil || user == nil {
			continue
		}

		if notified[user.Email] {
			continue
		}

		var sendErr error
		switch req.State {
		case "merged":
			sendErr = h.email.SendReviewMerged(user.Email, req.ActorName, req.ReviewTitle, reviewURL, req.Org, req.Repo, req.TargetBranch)
		case "abandoned":
			sendErr = h.email.SendReviewAbandoned(user.Email, req.ActorName, req.ReviewTitle, reviewURL, req.Org, req.Repo, req.Reason)
		case "approved":
			sendErr = h.email.SendReviewApproved(user.Email, req.ActorName, req.ReviewTitle, reviewURL, req.Org, req.Repo)
		case "ready":
			sendErr = h.email.SendReviewReady(user.Email, req.ActorName, req.ReviewTitle, reviewURL, req.Org, req.Repo)
		}

		if sendErr != nil {
			log.Printf("notify: failed to send %s email to %s: %v", req.State, user.Email, sendErr)
		} else {
			sentTo = append(sentTo, user.Email)
			notified[recipient] = true
			notified[user.Email] = true
		}
	}

	if len(sentTo) == 0 {
		writeJSON(w, http.StatusOK, map[string]string{"status": "skipped", "reason": "no recipients"})
		return
	}

	log.Printf("notify: sent %s notifications to %v for %s/%s/%s", req.State, sentTo, req.Org, req.Repo, req.ReviewID)
	writeJSON(w, http.StatusOK, map[string]interface{}{"status": "sent", "to": sentTo})
}
