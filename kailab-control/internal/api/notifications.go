package api

import (
	"encoding/json"
	"log"
	"net/http"
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
