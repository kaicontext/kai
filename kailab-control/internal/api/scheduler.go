package api

import (
	"encoding/json"
	"log"
	"time"

	"kailab-control/internal/cron"
	"kailab-control/internal/model"
	"kailab-control/internal/workflow"
)

// StartScheduler starts a background goroutine that checks for and triggers
// scheduled workflows every minute.
func (h *Handler) StartScheduler(done <-chan struct{}) {
	go func() {
		// Align to the start of the next minute for predictable scheduling.
		now := time.Now()
		next := now.Truncate(time.Minute).Add(time.Minute)
		time.Sleep(next.Sub(now))

		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()

		// Run immediately at the aligned minute, then on each tick.
		h.runScheduledWorkflows()

		for {
			select {
			case <-ticker.C:
				h.runScheduledWorkflows()
			case <-done:
				return
			}
		}
	}()
}

// runScheduledWorkflows checks all schedule-triggered workflows and creates runs
// for any whose cron expressions match the current minute.
func (h *Handler) runScheduledWorkflows() {
	now := time.Now().UTC()

	workflows, err := h.db.ListAllScheduleWorkflows()
	if err != nil {
		log.Printf("scheduler: failed to list schedule workflows: %v", err)
		return
	}

	if len(workflows) == 0 {
		return
	}

	for _, wf := range workflows {
		h.checkAndTriggerSchedule(wf, now)
	}
}

// checkAndTriggerSchedule checks if a workflow's schedule matches the current time
// and creates a run if so.
func (h *Handler) checkAndTriggerSchedule(wf *model.Workflow, now time.Time) {
	parsed, err := workflow.FromJSON(wf.ParsedJSON)
	if err != nil {
		log.Printf("scheduler: failed to parse workflow %s: %v", wf.ID, err)
		return
	}

	if len(parsed.On.Schedule) == 0 {
		return
	}

	// Check each cron expression
	for _, sched := range parsed.On.Schedule {
		cronSched, err := cron.Parse(sched.Cron)
		if err != nil {
			log.Printf("scheduler: invalid cron expression %q in workflow %s: %v", sched.Cron, wf.ID, err)
			continue
		}

		if !cronSched.Match(now) {
			continue
		}

		// This schedule matches — create a run.
		log.Printf("scheduler: triggering workflow %s (%s) on cron %q", wf.Name, wf.ID, sched.Cron)

		// Get repo and org info for the ref.
		repo, err := h.db.GetRepoByID(wf.RepoID)
		if err != nil {
			log.Printf("scheduler: failed to get repo %s: %v", wf.RepoID, err)
			return
		}
		org, err := h.db.GetOrgByID(repo.OrgID)
		if err != nil {
			log.Printf("scheduler: failed to get org %s: %v", repo.OrgID, err)
			return
		}

		// Schedule triggers use the default branch.
		ref := "refs/heads/main"

		payload := map[string]interface{}{
			"schedule": sched.Cron,
		}
		payloadJSON, _ := json.Marshal(payload)

		run, err := h.db.CreateWorkflowRun(wf.ID, repo.ID, model.TriggerSchedule, ref, "", string(payloadJSON), "")
		if err != nil {
			log.Printf("scheduler: failed to create run for workflow %s: %v", wf.ID, err)
			return
		}

		if err := h.createJobsFromWorkflow(wf, run); err != nil {
			log.Printf("scheduler: failed to create jobs for run %s: %v", run.ID, err)
			return
		}

		log.Printf("scheduler: created run %s for %s/%s workflow %s", run.ID, org.Slug, repo.Name, wf.Name)

		// Only trigger once per workflow per tick (first matching cron wins).
		return
	}
}
