package db

import (
	"database/sql"
	"encoding/json"
	"time"

	"kailab-control/internal/model"
)

// ----- Workflows -----

// CreateWorkflow creates a new workflow.
func (db *DB) CreateWorkflow(repoID, path, name, contentHash, parsedJSON string, triggers []string) (*model.Workflow, error) {
	id := newUUID()
	now := time.Now().Unix()
	triggersJSON, _ := json.Marshal(triggers)

	// PostgreSQL expects boolean true, SQLite expects integer 1
	var activeVal interface{}
	if db.driver == DriverPostgres {
		activeVal = true
	} else {
		activeVal = 1
	}

	_, err := db.exec(
		"INSERT INTO workflows (id, repo_id, path, name, content_hash, parsed_json, triggers, active, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		id, repoID, path, name, contentHash, parsedJSON, string(triggersJSON), activeVal, now, now,
	)
	if err != nil {
		return nil, err
	}
	return db.GetWorkflowByID(id)
}

// GetWorkflowByID retrieves a workflow by ID.
func (db *DB) GetWorkflowByID(id string) (*model.Workflow, error) {
	var w model.Workflow
	var triggersJSON string
	var active bool
	var createdAt, updatedAt int64

	err := db.queryRow(
		"SELECT id, repo_id, path, name, content_hash, parsed_json, triggers, active, created_at, updated_at FROM workflows WHERE id = ?",
		id,
	).Scan(&w.ID, &w.RepoID, &w.Path, &w.Name, &w.ContentHash, &w.ParsedJSON, &triggersJSON, &active, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	w.Active = active
	w.CreatedAt = time.Unix(createdAt, 0)
	w.UpdatedAt = time.Unix(updatedAt, 0)
	json.Unmarshal([]byte(triggersJSON), &w.Triggers)
	return &w, nil
}

// GetWorkflowByRepoAndPath retrieves a workflow by repo ID and path.
func (db *DB) GetWorkflowByRepoAndPath(repoID, path string) (*model.Workflow, error) {
	var w model.Workflow
	var triggersJSON string
	var active bool
	var createdAt, updatedAt int64

	err := db.queryRow(
		"SELECT id, repo_id, path, name, content_hash, parsed_json, triggers, active, created_at, updated_at FROM workflows WHERE repo_id = ? AND path = ?",
		repoID, path,
	).Scan(&w.ID, &w.RepoID, &w.Path, &w.Name, &w.ContentHash, &w.ParsedJSON, &triggersJSON, &active, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	w.Active = active
	w.CreatedAt = time.Unix(createdAt, 0)
	w.UpdatedAt = time.Unix(updatedAt, 0)
	json.Unmarshal([]byte(triggersJSON), &w.Triggers)
	return &w, nil
}

// ListRepoWorkflows lists all workflows for a repository.
func (db *DB) ListRepoWorkflows(repoID string) ([]*model.Workflow, error) {
	rows, err := db.query(
		"SELECT id, repo_id, path, name, content_hash, parsed_json, triggers, active, created_at, updated_at FROM workflows WHERE repo_id = ? ORDER BY name",
		repoID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var workflows []*model.Workflow
	for rows.Next() {
		var w model.Workflow
		var triggersJSON string
		var active bool
		var createdAt, updatedAt int64

		if err := rows.Scan(&w.ID, &w.RepoID, &w.Path, &w.Name, &w.ContentHash, &w.ParsedJSON, &triggersJSON, &active, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		w.Active = active
		w.CreatedAt = time.Unix(createdAt, 0)
		w.UpdatedAt = time.Unix(updatedAt, 0)
		json.Unmarshal([]byte(triggersJSON), &w.Triggers)
		workflows = append(workflows, &w)
	}
	return workflows, rows.Err()
}

// ListActiveWorkflowsByTrigger lists active workflows that match a trigger type.
func (db *DB) ListActiveWorkflowsByTrigger(repoID, trigger string) ([]*model.Workflow, error) {
	// JSON array search - works for both SQLite and PostgreSQL
	// Note: PostgreSQL uses BOOLEAN for active column, SQLite uses INTEGER
	var query string
	if db.driver == DriverPostgres {
		query = "SELECT id, repo_id, path, name, content_hash, parsed_json, triggers, active, created_at, updated_at FROM workflows WHERE repo_id = ? AND active = true AND triggers LIKE ?"
	} else {
		query = "SELECT id, repo_id, path, name, content_hash, parsed_json, triggers, active, created_at, updated_at FROM workflows WHERE repo_id = ? AND active = 1 AND triggers LIKE ?"
	}
	rows, err := db.query(
		query,
		repoID, "%\""+trigger+"\"%",
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var workflows []*model.Workflow
	for rows.Next() {
		var w model.Workflow
		var triggersJSON string
		var active bool
		var createdAt, updatedAt int64

		if err := rows.Scan(&w.ID, &w.RepoID, &w.Path, &w.Name, &w.ContentHash, &w.ParsedJSON, &triggersJSON, &active, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		w.Active = active
		w.CreatedAt = time.Unix(createdAt, 0)
		w.UpdatedAt = time.Unix(updatedAt, 0)
		json.Unmarshal([]byte(triggersJSON), &w.Triggers)
		workflows = append(workflows, &w)
	}
	return workflows, rows.Err()
}

// ListAllScheduleWorkflows lists all active workflows with a schedule trigger across all repos.
func (db *DB) ListAllScheduleWorkflows() ([]*model.Workflow, error) {
	var query string
	if db.driver == DriverPostgres {
		query = "SELECT id, repo_id, path, name, content_hash, parsed_json, triggers, active, created_at, updated_at FROM workflows WHERE active = true AND triggers LIKE ?"
	} else {
		query = "SELECT id, repo_id, path, name, content_hash, parsed_json, triggers, active, created_at, updated_at FROM workflows WHERE active = 1 AND triggers LIKE ?"
	}
	rows, err := db.query(query, "%\"schedule\"%")
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var workflows []*model.Workflow
	for rows.Next() {
		var w model.Workflow
		var triggersJSON string
		var active bool
		var createdAt, updatedAt int64

		if err := rows.Scan(&w.ID, &w.RepoID, &w.Path, &w.Name, &w.ContentHash, &w.ParsedJSON, &triggersJSON, &active, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		w.Active = active
		w.CreatedAt = time.Unix(createdAt, 0)
		w.UpdatedAt = time.Unix(updatedAt, 0)
		json.Unmarshal([]byte(triggersJSON), &w.Triggers)
		workflows = append(workflows, &w)
	}
	return workflows, rows.Err()
}

// UpdateWorkflow updates a workflow.
func (db *DB) UpdateWorkflow(id, name, contentHash, parsedJSON string, triggers []string, active bool) error {
	now := time.Now().Unix()
	triggersJSON, _ := json.Marshal(triggers)

	// PostgreSQL expects boolean, SQLite expects integer
	var activeVal interface{}
	if db.driver == DriverPostgres {
		activeVal = active
	} else {
		activeInt := 0
		if active {
			activeInt = 1
		}
		activeVal = activeInt
	}

	_, err := db.exec(
		"UPDATE workflows SET name = ?, content_hash = ?, parsed_json = ?, triggers = ?, active = ?, updated_at = ? WHERE id = ?",
		name, contentHash, parsedJSON, string(triggersJSON), activeVal, now, id,
	)
	return err
}

// DeleteWorkflow deletes a workflow.
func (db *DB) DeleteWorkflow(id string) error {
	_, err := db.exec("DELETE FROM workflows WHERE id = ?", id)
	return err
}

// ----- Workflow Runs -----

// CreateWorkflowRun creates a new workflow run.
func (db *DB) CreateWorkflowRun(workflowID, repoID, triggerEvent, triggerRef, triggerSHA, triggerPayload, createdBy string) (*model.WorkflowRun, error) {
	id := newUUID()
	now := time.Now().Unix()

	// Get next run number for this workflow
	var runNumber int
	err := db.queryRow(
		"SELECT COALESCE(MAX(run_number), 0) + 1 FROM workflow_runs WHERE workflow_id = ?",
		workflowID,
	).Scan(&runNumber)
	if err != nil {
		return nil, err
	}

	var createdByPtr interface{}
	if createdBy != "" {
		createdByPtr = createdBy
	}

	_, err = db.exec(
		"INSERT INTO workflow_runs (id, workflow_id, repo_id, run_number, trigger_event, trigger_ref, trigger_sha, trigger_payload, status, created_at, created_by) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
		id, workflowID, repoID, runNumber, triggerEvent, triggerRef, triggerSHA, triggerPayload, model.RunStatusQueued, now, createdByPtr,
	)
	if err != nil {
		return nil, err
	}
	return db.GetWorkflowRunByID(id)
}

// GetWorkflowRunByID retrieves a workflow run by ID.
func (db *DB) GetWorkflowRunByID(id string) (*model.WorkflowRun, error) {
	var r model.WorkflowRun
	var startedAt, completedAt sql.NullInt64
	var createdAt int64
	var conclusion, triggerRef, triggerSHA, createdBy sql.NullString

	err := db.queryRow(
		"SELECT id, workflow_id, repo_id, run_number, trigger_event, trigger_ref, trigger_sha, trigger_payload, status, conclusion, started_at, completed_at, created_at, created_by FROM workflow_runs WHERE id = ?",
		id,
	).Scan(&r.ID, &r.WorkflowID, &r.RepoID, &r.RunNumber, &r.TriggerEvent, &triggerRef, &triggerSHA, &r.TriggerPayload, &r.Status, &conclusion, &startedAt, &completedAt, &createdAt, &createdBy)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	r.CreatedAt = time.Unix(createdAt, 0)
	if startedAt.Valid {
		r.StartedAt = time.Unix(startedAt.Int64, 0)
	}
	if completedAt.Valid {
		r.CompletedAt = time.Unix(completedAt.Int64, 0)
	}
	if conclusion.Valid {
		r.Conclusion = conclusion.String
	}
	if triggerRef.Valid {
		r.TriggerRef = triggerRef.String
	}
	if triggerSHA.Valid {
		r.TriggerSHA = triggerSHA.String
	}
	if createdBy.Valid {
		r.CreatedBy = createdBy.String
	}
	return &r, nil
}

// ListRepoWorkflowRuns lists workflow runs for a repository.
func (db *DB) ListRepoWorkflowRuns(repoID string, limit int) ([]*model.WorkflowRunWithDetails, error) {
	rows, err := db.query(`
		SELECT r.id, r.workflow_id, r.repo_id, r.run_number, r.trigger_event, r.trigger_ref, r.trigger_sha, r.trigger_payload, r.status, r.conclusion, r.started_at, r.completed_at, r.created_at, r.created_by, w.name, w.path
		FROM workflow_runs r
		JOIN workflows w ON w.id = r.workflow_id
		WHERE r.repo_id = ?
		ORDER BY r.created_at DESC
		LIMIT ?
	`, repoID, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runs []*model.WorkflowRunWithDetails
	for rows.Next() {
		var r model.WorkflowRunWithDetails
		var startedAt, completedAt sql.NullInt64
		var createdAt int64
		var conclusion, triggerRef, triggerSHA, createdBy sql.NullString

		if err := rows.Scan(&r.ID, &r.WorkflowID, &r.RepoID, &r.RunNumber, &r.TriggerEvent, &triggerRef, &triggerSHA, &r.TriggerPayload, &r.Status, &conclusion, &startedAt, &completedAt, &createdAt, &createdBy, &r.WorkflowName, &r.WorkflowPath); err != nil {
			return nil, err
		}
		r.CreatedAt = time.Unix(createdAt, 0)
		if startedAt.Valid {
			r.StartedAt = time.Unix(startedAt.Int64, 0)
		}
		if completedAt.Valid {
			r.CompletedAt = time.Unix(completedAt.Int64, 0)
		}
		if conclusion.Valid {
			r.Conclusion = conclusion.String
		}
		if triggerRef.Valid {
			r.TriggerRef = triggerRef.String
		}
		if triggerSHA.Valid {
			r.TriggerSHA = triggerSHA.String
		}
		if createdBy.Valid {
			r.CreatedBy = createdBy.String
		}
		runs = append(runs, &r)
	}
	return runs, rows.Err()
}

// UpdateWorkflowRunStatus updates the status of a workflow run.
func (db *DB) UpdateWorkflowRunStatus(id, status string) error {
	now := time.Now().Unix()
	if status == model.RunStatusInProgress {
		_, err := db.exec("UPDATE workflow_runs SET status = ?, started_at = ? WHERE id = ?", status, now, id)
		return err
	}
	_, err := db.exec("UPDATE workflow_runs SET status = ? WHERE id = ?", status, id)
	return err
}

// CompleteWorkflowRun marks a workflow run as completed.
func (db *DB) CompleteWorkflowRun(id, conclusion string) error {
	now := time.Now().Unix()
	_, err := db.exec("UPDATE workflow_runs SET status = ?, conclusion = ?, completed_at = ? WHERE id = ?",
		model.RunStatusCompleted, conclusion, now, id)
	return err
}

// ----- Jobs -----

// CreateJob creates a new job.
func (db *DB) CreateJob(workflowRunID, name string, needs []string, matrixValues string, runsOn []string) (*model.Job, error) {
	id := newUUID()
	now := time.Now().Unix()
	needsJSON, _ := json.Marshal(needs)
	runsOnJSON, _ := json.Marshal(runsOn)

	var matrixPtr interface{}
	if matrixValues != "" {
		matrixPtr = matrixValues
	}

	_, err := db.exec(
		"INSERT INTO jobs (id, workflow_run_id, name, status, needs, matrix_values, runs_on, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		id, workflowRunID, name, model.JobStatusQueued, string(needsJSON), matrixPtr, string(runsOnJSON), now,
	)
	if err != nil {
		return nil, err
	}
	return db.GetJobByID(id)
}

// GetJobByID retrieves a job by ID.
func (db *DB) GetJobByID(id string) (*model.Job, error) {
	var j model.Job
	var startedAt, completedAt sql.NullInt64
	var createdAt int64
	var conclusion, runnerID, runnerName, matrixValues, needsJSON, runsOnJSON sql.NullString

	err := db.queryRow(
		"SELECT id, workflow_run_id, name, runner_id, status, conclusion, matrix_values, needs, runner_name, started_at, completed_at, created_at, runs_on FROM jobs WHERE id = ?",
		id,
	).Scan(&j.ID, &j.WorkflowRunID, &j.Name, &runnerID, &j.Status, &conclusion, &matrixValues, &needsJSON, &runnerName, &startedAt, &completedAt, &createdAt, &runsOnJSON)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	j.CreatedAt = time.Unix(createdAt, 0)
	if startedAt.Valid {
		j.StartedAt = time.Unix(startedAt.Int64, 0)
	}
	if completedAt.Valid {
		j.CompletedAt = time.Unix(completedAt.Int64, 0)
	}
	if conclusion.Valid {
		j.Conclusion = conclusion.String
	}
	if runnerID.Valid {
		j.RunnerID = runnerID.String
	}
	if runnerName.Valid {
		j.RunnerName = runnerName.String
	}
	if matrixValues.Valid {
		j.MatrixValues = matrixValues.String
	}
	if needsJSON.Valid {
		json.Unmarshal([]byte(needsJSON.String), &j.Needs)
	}
	if runsOnJSON.Valid {
		json.Unmarshal([]byte(runsOnJSON.String), &j.RunsOn)
	}
	return &j, nil
}

// ListWorkflowRunJobs lists all jobs for a workflow run.
func (db *DB) ListWorkflowRunJobs(workflowRunID string) ([]*model.Job, error) {
	rows, err := db.query(
		"SELECT id, workflow_run_id, name, runner_id, status, conclusion, matrix_values, needs, runner_name, started_at, completed_at, created_at, runs_on FROM jobs WHERE workflow_run_id = ? ORDER BY created_at",
		workflowRunID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var jobs []*model.Job
	for rows.Next() {
		var j model.Job
		var startedAt, completedAt sql.NullInt64
		var createdAt int64
		var conclusion, runnerID, runnerName, matrixValues, needsJSON, runsOnJSON sql.NullString

		if err := rows.Scan(&j.ID, &j.WorkflowRunID, &j.Name, &runnerID, &j.Status, &conclusion, &matrixValues, &needsJSON, &runnerName, &startedAt, &completedAt, &createdAt, &runsOnJSON); err != nil {
			return nil, err
		}
		j.CreatedAt = time.Unix(createdAt, 0)
		if startedAt.Valid {
			j.StartedAt = time.Unix(startedAt.Int64, 0)
		}
		if completedAt.Valid {
			j.CompletedAt = time.Unix(completedAt.Int64, 0)
		}
		if conclusion.Valid {
			j.Conclusion = conclusion.String
		}
		if runnerID.Valid {
			j.RunnerID = runnerID.String
		}
		if runnerName.Valid {
			j.RunnerName = runnerName.String
		}
		if matrixValues.Valid {
			j.MatrixValues = matrixValues.String
		}
		if needsJSON.Valid {
			json.Unmarshal([]byte(needsJSON.String), &j.Needs)
		}
		if runsOnJSON.Valid {
			json.Unmarshal([]byte(runsOnJSON.String), &j.RunsOn)
		}
		jobs = append(jobs, &j)
	}
	return jobs, rows.Err()
}

// ClaimJob assigns a job to a runner. Returns nil if no jobs are available.
func (db *DB) ClaimJob(runnerID string, labels []string) (*model.Job, error) {
	// Find the first queued job that:
	// 1. Has all dependencies completed successfully
	// 2. Matches the runner's labels (all job runs_on labels must be in runner labels)
	labelsJSON, _ := json.Marshal(labels)

	var query string
	if db.driver == DriverPostgres {
		query = `
			SELECT j.id FROM jobs j
			WHERE j.status = $1
			AND (j.needs IS NULL OR j.needs = '[]' OR j.needs = ''
				OR jsonb_typeof(j.needs::jsonb) != 'array'
				OR NOT EXISTS (
					SELECT 1 FROM jobs dep
					WHERE dep.workflow_run_id = j.workflow_run_id
					AND dep.name IN (SELECT jsonb_array_elements_text(j.needs::jsonb))
					AND dep.status != $2
				))
			AND (j.runs_on IS NULL OR j.runs_on = '[]' OR j.runs_on = ''
				OR NOT EXISTS (
					SELECT 1 FROM jsonb_array_elements_text(j.runs_on::jsonb) AS required_label
					WHERE required_label NOT IN (SELECT jsonb_array_elements_text($3::jsonb))
				))
			ORDER BY j.created_at
			LIMIT 1
		`
	} else {
		query = `
			SELECT j.id FROM jobs j
			WHERE j.status = ?
			AND (j.needs IS NULL OR j.needs = '[]' OR j.needs = '' OR NOT EXISTS (
				SELECT 1 FROM jobs dep
				WHERE dep.workflow_run_id = j.workflow_run_id
				AND dep.name IN (SELECT value FROM json_each(j.needs))
				AND dep.status != ?
			))
			AND (j.runs_on IS NULL OR j.runs_on = '[]' OR j.runs_on = '' OR NOT EXISTS (
				SELECT 1 FROM json_each(j.runs_on) AS required
				WHERE required.value NOT IN (SELECT value FROM json_each(?))
			))
			ORDER BY j.created_at
			LIMIT 1
		`
	}
	row := db.queryRow(query, model.JobStatusQueued, model.JobStatusCompleted, string(labelsJSON))

	var jobID string
	if err := row.Scan(&jobID); err == sql.ErrNoRows {
		return nil, nil // No jobs available
	} else if err != nil {
		return nil, err
	}

	// Try to claim the job
	result, err := db.exec(
		"UPDATE jobs SET runner_id = ?, status = ? WHERE id = ? AND status = ?",
		runnerID, model.JobStatusPending, jobID, model.JobStatusQueued,
	)
	if err != nil {
		return nil, err
	}

	affected, _ := result.RowsAffected()
	if affected == 0 {
		return nil, nil // Job was claimed by another runner
	}

	return db.GetJobByID(jobID)
}

// StartJob marks a job as in progress.
func (db *DB) StartJob(id, runnerName string) error {
	now := time.Now().Unix()
	_, err := db.exec("UPDATE jobs SET status = ?, runner_name = ?, started_at = ? WHERE id = ?",
		model.JobStatusInProgress, runnerName, now, id)
	return err
}

// CompleteJob marks a job as completed.
func (db *DB) CompleteJob(id, conclusion string) error {
	now := time.Now().Unix()
	_, err := db.exec("UPDATE jobs SET status = ?, conclusion = ?, completed_at = ? WHERE id = ?",
		model.JobStatusCompleted, conclusion, now, id)
	return err
}

// SetJobOutputs stores the evaluated outputs for a completed job.
func (db *DB) SetJobOutputs(id string, outputs map[string]string) error {
	b, _ := json.Marshal(outputs)
	_, err := db.exec("UPDATE jobs SET outputs = ? WHERE id = ?", string(b), id)
	return err
}

// GetJobOutputs retrieves the outputs for a job.
func (db *DB) GetJobOutputs(id string) (map[string]string, error) {
	var outputsJSON sql.NullString
	err := db.queryRow("SELECT outputs FROM jobs WHERE id = ?", id).Scan(&outputsJSON)
	if err != nil {
		return nil, err
	}
	result := make(map[string]string)
	if outputsJSON.Valid && outputsJSON.String != "" {
		json.Unmarshal([]byte(outputsJSON.String), &result)
	}
	return result, nil
}

// SetJobSummary stores the step summary markdown for a job.
func (db *DB) SetJobSummary(id, summary string) error {
	_, err := db.exec("UPDATE jobs SET summary = ? WHERE id = ?", summary, id)
	return err
}

// GetJobSummary retrieves the step summary for a job.
func (db *DB) GetJobSummary(id string) (string, error) {
	var summary sql.NullString
	err := db.queryRow("SELECT summary FROM jobs WHERE id = ?", id).Scan(&summary)
	if err != nil {
		return "", err
	}
	if summary.Valid {
		return summary.String, nil
	}
	return "", nil
}

// ----- Steps -----

// CreateStep creates a new step.
func (db *DB) CreateStep(jobID string, number int, name string) (*model.Step, error) {
	id := newUUID()
	_, err := db.exec(
		"INSERT INTO steps (id, job_id, number, name, status) VALUES (?, ?, ?, ?, ?)",
		id, jobID, number, name, model.StepStatusPending,
	)
	if err != nil {
		return nil, err
	}
	return db.GetStepByID(id)
}

// GetStepByID retrieves a step by ID.
func (db *DB) GetStepByID(id string) (*model.Step, error) {
	var s model.Step
	var startedAt, completedAt sql.NullInt64
	var conclusion sql.NullString

	err := db.queryRow(
		"SELECT id, job_id, number, name, status, conclusion, started_at, completed_at FROM steps WHERE id = ?",
		id,
	).Scan(&s.ID, &s.JobID, &s.Number, &s.Name, &s.Status, &conclusion, &startedAt, &completedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	if startedAt.Valid {
		s.StartedAt = time.Unix(startedAt.Int64, 0)
	}
	if completedAt.Valid {
		s.CompletedAt = time.Unix(completedAt.Int64, 0)
	}
	if conclusion.Valid {
		s.Conclusion = conclusion.String
	}
	return &s, nil
}

// GetStepByJobAndNumber retrieves a step by job ID and number.
func (db *DB) GetStepByJobAndNumber(jobID string, number int) (*model.Step, error) {
	var s model.Step
	var startedAt, completedAt sql.NullInt64
	var conclusion sql.NullString

	err := db.queryRow(
		"SELECT id, job_id, number, name, status, conclusion, started_at, completed_at FROM steps WHERE job_id = ? AND number = ?",
		jobID, number,
	).Scan(&s.ID, &s.JobID, &s.Number, &s.Name, &s.Status, &conclusion, &startedAt, &completedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	if startedAt.Valid {
		s.StartedAt = time.Unix(startedAt.Int64, 0)
	}
	if completedAt.Valid {
		s.CompletedAt = time.Unix(completedAt.Int64, 0)
	}
	if conclusion.Valid {
		s.Conclusion = conclusion.String
	}
	return &s, nil
}

// ListJobSteps lists all steps for a job.
func (db *DB) ListJobSteps(jobID string) ([]*model.Step, error) {
	rows, err := db.query(
		"SELECT id, job_id, number, name, status, conclusion, started_at, completed_at FROM steps WHERE job_id = ? ORDER BY number",
		jobID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var steps []*model.Step
	for rows.Next() {
		var s model.Step
		var startedAt, completedAt sql.NullInt64
		var conclusion sql.NullString

		if err := rows.Scan(&s.ID, &s.JobID, &s.Number, &s.Name, &s.Status, &conclusion, &startedAt, &completedAt); err != nil {
			return nil, err
		}
		if startedAt.Valid {
			s.StartedAt = time.Unix(startedAt.Int64, 0)
		}
		if completedAt.Valid {
			s.CompletedAt = time.Unix(completedAt.Int64, 0)
		}
		if conclusion.Valid {
			s.Conclusion = conclusion.String
		}
		steps = append(steps, &s)
	}
	return steps, rows.Err()
}

// StartStep marks a step as in progress.
func (db *DB) StartStep(id string) error {
	now := time.Now().Unix()
	_, err := db.exec("UPDATE steps SET status = ?, started_at = ? WHERE id = ?",
		model.StepStatusInProgress, now, id)
	return err
}

// CompleteStep marks a step as completed.
func (db *DB) CompleteStep(id, conclusion string) error {
	now := time.Now().Unix()
	_, err := db.exec("UPDATE steps SET status = ?, conclusion = ?, completed_at = ? WHERE id = ?",
		model.StepStatusCompleted, conclusion, now, id)
	return err
}

// ----- Job Logs -----

// AppendJobLog appends a log chunk to a job.
func (db *DB) AppendJobLog(jobID, stepID, content string) error {
	id := newUUID()
	now := time.Now().Unix()

	// Get next chunk sequence
	var chunkSeq int
	err := db.queryRow(
		"SELECT COALESCE(MAX(chunk_seq), -1) + 1 FROM job_logs WHERE job_id = ?",
		jobID,
	).Scan(&chunkSeq)
	if err != nil {
		return err
	}

	var stepIDPtr interface{}
	if stepID != "" {
		stepIDPtr = stepID
	}

	_, err = db.exec(
		"INSERT INTO job_logs (id, job_id, step_id, chunk_seq, content, timestamp) VALUES (?, ?, ?, ?, ?, ?)",
		id, jobID, stepIDPtr, chunkSeq, content, now,
	)
	return err
}

// GetJobLogs retrieves all log chunks for a job.
func (db *DB) GetJobLogs(jobID string) ([]*model.JobLog, error) {
	rows, err := db.query(
		"SELECT id, job_id, step_id, chunk_seq, content, timestamp FROM job_logs WHERE job_id = ? ORDER BY chunk_seq",
		jobID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []*model.JobLog
	for rows.Next() {
		var l model.JobLog
		var timestamp int64
		var stepID sql.NullString

		if err := rows.Scan(&l.ID, &l.JobID, &stepID, &l.ChunkSeq, &l.Content, &timestamp); err != nil {
			return nil, err
		}
		l.Timestamp = time.Unix(timestamp, 0)
		if stepID.Valid {
			l.StepID = stepID.String
		}
		logs = append(logs, &l)
	}
	return logs, rows.Err()
}

// GetJobLogsSince retrieves log chunks after a given sequence number.
func (db *DB) GetJobLogsSince(jobID string, afterSeq int) ([]*model.JobLog, error) {
	rows, err := db.query(
		"SELECT id, job_id, step_id, chunk_seq, content, timestamp FROM job_logs WHERE job_id = ? AND chunk_seq > ? ORDER BY chunk_seq",
		jobID, afterSeq,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var logs []*model.JobLog
	for rows.Next() {
		var l model.JobLog
		var timestamp int64
		var stepID sql.NullString

		if err := rows.Scan(&l.ID, &l.JobID, &stepID, &l.ChunkSeq, &l.Content, &timestamp); err != nil {
			return nil, err
		}
		l.Timestamp = time.Unix(timestamp, 0)
		if stepID.Valid {
			l.StepID = stepID.String
		}
		logs = append(logs, &l)
	}
	return logs, rows.Err()
}

// ----- Artifacts -----

// CreateArtifact creates a new artifact.
func (db *DB) CreateArtifact(workflowRunID, jobID, name, path string, size int64, expiresAt *time.Time) (*model.Artifact, error) {
	id := newUUID()
	now := time.Now().Unix()

	var expiresAtPtr interface{}
	if expiresAt != nil {
		expiresAtPtr = expiresAt.Unix()
	}

	_, err := db.exec(
		"INSERT INTO artifacts (id, workflow_run_id, job_id, name, path, size, expires_at, created_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		id, workflowRunID, jobID, name, path, size, expiresAtPtr, now,
	)
	if err != nil {
		return nil, err
	}
	return db.GetArtifactByID(id)
}

// GetArtifactByID retrieves an artifact by ID.
func (db *DB) GetArtifactByID(id string) (*model.Artifact, error) {
	var a model.Artifact
	var createdAt int64
	var expiresAt sql.NullInt64

	err := db.queryRow(
		"SELECT id, workflow_run_id, job_id, name, path, size, expires_at, created_at FROM artifacts WHERE id = ?",
		id,
	).Scan(&a.ID, &a.WorkflowRunID, &a.JobID, &a.Name, &a.Path, &a.Size, &expiresAt, &createdAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	a.CreatedAt = time.Unix(createdAt, 0)
	if expiresAt.Valid {
		a.ExpiresAt = time.Unix(expiresAt.Int64, 0)
	}
	return &a, nil
}

// ListWorkflowRunArtifacts lists all artifacts for a workflow run.
func (db *DB) ListWorkflowRunArtifacts(workflowRunID string) ([]*model.Artifact, error) {
	rows, err := db.query(
		"SELECT id, workflow_run_id, job_id, name, path, size, expires_at, created_at FROM artifacts WHERE workflow_run_id = ? ORDER BY created_at",
		workflowRunID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var artifacts []*model.Artifact
	for rows.Next() {
		var a model.Artifact
		var createdAt int64
		var expiresAt sql.NullInt64

		if err := rows.Scan(&a.ID, &a.WorkflowRunID, &a.JobID, &a.Name, &a.Path, &a.Size, &expiresAt, &createdAt); err != nil {
			return nil, err
		}
		a.CreatedAt = time.Unix(createdAt, 0)
		if expiresAt.Valid {
			a.ExpiresAt = time.Unix(expiresAt.Int64, 0)
		}
		artifacts = append(artifacts, &a)
	}
	return artifacts, rows.Err()
}

// ----- Workflow Secrets -----

// CreateWorkflowSecret creates a new workflow secret.
func (db *DB) CreateWorkflowSecret(repoID, orgID, name string, encrypted []byte, createdBy string) (*model.WorkflowSecret, error) {
	id := newUUID()
	now := time.Now().Unix()

	var repoIDPtr, orgIDPtr interface{}
	if repoID != "" {
		repoIDPtr = repoID
	}
	if orgID != "" {
		orgIDPtr = orgID
	}

	_, err := db.exec(
		"INSERT INTO workflow_secrets (id, repo_id, org_id, name, encrypted, created_at, updated_at, created_by) VALUES (?, ?, ?, ?, ?, ?, ?, ?)",
		id, repoIDPtr, orgIDPtr, name, encrypted, now, now, createdBy,
	)
	if err != nil {
		return nil, err
	}
	return db.GetWorkflowSecretByID(id)
}

// GetWorkflowSecretByID retrieves a workflow secret by ID.
func (db *DB) GetWorkflowSecretByID(id string) (*model.WorkflowSecret, error) {
	var s model.WorkflowSecret
	var createdAt, updatedAt int64
	var repoID, orgID sql.NullString

	err := db.queryRow(
		"SELECT id, repo_id, org_id, name, encrypted, created_at, updated_at, created_by FROM workflow_secrets WHERE id = ?",
		id,
	).Scan(&s.ID, &repoID, &orgID, &s.Name, &s.Encrypted, &createdAt, &updatedAt, &s.CreatedBy)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	s.CreatedAt = time.Unix(createdAt, 0)
	s.UpdatedAt = time.Unix(updatedAt, 0)
	if repoID.Valid {
		s.RepoID = repoID.String
	}
	if orgID.Valid {
		s.OrgID = orgID.String
	}
	return &s, nil
}

// GetWorkflowSecretByName retrieves a workflow secret by name for a repo or org.
func (db *DB) GetWorkflowSecretByName(repoID, orgID, name string) (*model.WorkflowSecret, error) {
	var s model.WorkflowSecret
	var createdAt, updatedAt int64
	var repoIDNull, orgIDNull sql.NullString

	var query string
	var args []interface{}
	if repoID != "" {
		query = "SELECT id, repo_id, org_id, name, encrypted, created_at, updated_at, created_by FROM workflow_secrets WHERE repo_id = ? AND name = ?"
		args = []interface{}{repoID, name}
	} else if orgID != "" {
		query = "SELECT id, repo_id, org_id, name, encrypted, created_at, updated_at, created_by FROM workflow_secrets WHERE org_id = ? AND repo_id IS NULL AND name = ?"
		args = []interface{}{orgID, name}
	} else {
		return nil, ErrNotFound
	}

	err := db.queryRow(query, args...).Scan(&s.ID, &repoIDNull, &orgIDNull, &s.Name, &s.Encrypted, &createdAt, &updatedAt, &s.CreatedBy)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	s.CreatedAt = time.Unix(createdAt, 0)
	s.UpdatedAt = time.Unix(updatedAt, 0)
	if repoIDNull.Valid {
		s.RepoID = repoIDNull.String
	}
	if orgIDNull.Valid {
		s.OrgID = orgIDNull.String
	}
	return &s, nil
}

// ListRepoSecrets lists all secrets for a repo (includes org-level secrets).
func (db *DB) ListRepoSecrets(repoID, orgID string) ([]*model.WorkflowSecret, error) {
	rows, err := db.query(
		"SELECT id, repo_id, org_id, name, encrypted, created_at, updated_at, created_by FROM workflow_secrets WHERE repo_id = ? OR (org_id = ? AND repo_id IS NULL) ORDER BY name",
		repoID, orgID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var secrets []*model.WorkflowSecret
	for rows.Next() {
		var s model.WorkflowSecret
		var createdAt, updatedAt int64
		var repoIDNull, orgIDNull sql.NullString

		if err := rows.Scan(&s.ID, &repoIDNull, &orgIDNull, &s.Name, &s.Encrypted, &createdAt, &updatedAt, &s.CreatedBy); err != nil {
			return nil, err
		}
		s.CreatedAt = time.Unix(createdAt, 0)
		s.UpdatedAt = time.Unix(updatedAt, 0)
		if repoIDNull.Valid {
			s.RepoID = repoIDNull.String
		}
		if orgIDNull.Valid {
			s.OrgID = orgIDNull.String
		}
		secrets = append(secrets, &s)
	}
	return secrets, rows.Err()
}

// UpdateWorkflowSecret updates a workflow secret.
func (db *DB) UpdateWorkflowSecret(id string, encrypted []byte) error {
	now := time.Now().Unix()
	_, err := db.exec("UPDATE workflow_secrets SET encrypted = ?, updated_at = ? WHERE id = ?", encrypted, now, id)
	return err
}

// DeleteWorkflowSecret deletes a workflow secret.
func (db *DB) DeleteWorkflowSecret(id string) error {
	_, err := db.exec("DELETE FROM workflow_secrets WHERE id = ?", id)
	return err
}

// ----- Variables -----

// SetVariable creates or updates a workflow variable.
func (db *DB) SetVariable(repoID, orgID, name, value string) error {
	existing, _ := db.GetVariableByName(repoID, orgID, name)
	if existing != nil {
		now := time.Now().Unix()
		_, err := db.exec("UPDATE workflow_variables SET value = ?, updated_at = ? WHERE id = ?", value, now, existing.ID)
		return err
	}

	id := newUUID()
	if repoID != "" {
		_, err := db.exec(
			"INSERT INTO workflow_variables (id, repo_id, name, value) VALUES (?, ?, ?, ?)",
			id, repoID, name, value,
		)
		return err
	}
	_, err := db.exec(
		"INSERT INTO workflow_variables (id, org_id, name, value) VALUES (?, ?, ?, ?)",
		id, orgID, name, value,
	)
	return err
}

// GetVariableByName retrieves a variable by name.
func (db *DB) GetVariableByName(repoID, orgID, name string) (*model.WorkflowVariable, error) {
	var v model.WorkflowVariable
	var createdAt, updatedAt int64
	var repoIDNull, orgIDNull sql.NullString

	var query string
	var args []interface{}
	if repoID != "" {
		query = "SELECT id, repo_id, org_id, name, value, created_at, updated_at FROM workflow_variables WHERE repo_id = ? AND name = ?"
		args = []interface{}{repoID, name}
	} else if orgID != "" {
		query = "SELECT id, repo_id, org_id, name, value, created_at, updated_at FROM workflow_variables WHERE org_id = ? AND repo_id IS NULL AND name = ?"
		args = []interface{}{orgID, name}
	} else {
		return nil, ErrNotFound
	}

	err := db.queryRow(query, args...).Scan(&v.ID, &repoIDNull, &orgIDNull, &v.Name, &v.Value, &createdAt, &updatedAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	v.CreatedAt = time.Unix(createdAt, 0)
	v.UpdatedAt = time.Unix(updatedAt, 0)
	if repoIDNull.Valid {
		v.RepoID = repoIDNull.String
	}
	if orgIDNull.Valid {
		v.OrgID = orgIDNull.String
	}
	return &v, nil
}

// ListRepoVariables lists all variables for a repo (includes org-level variables).
func (db *DB) ListRepoVariables(repoID, orgID string) ([]*model.WorkflowVariable, error) {
	rows, err := db.query(
		"SELECT id, repo_id, org_id, name, value, created_at, updated_at FROM workflow_variables WHERE repo_id = ? OR (org_id = ? AND repo_id IS NULL) ORDER BY name",
		repoID, orgID,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var vars []*model.WorkflowVariable
	for rows.Next() {
		var v model.WorkflowVariable
		var createdAt, updatedAt int64
		var repoIDNull, orgIDNull sql.NullString

		if err := rows.Scan(&v.ID, &repoIDNull, &orgIDNull, &v.Name, &v.Value, &createdAt, &updatedAt); err != nil {
			return nil, err
		}
		v.CreatedAt = time.Unix(createdAt, 0)
		v.UpdatedAt = time.Unix(updatedAt, 0)
		if repoIDNull.Valid {
			v.RepoID = repoIDNull.String
		}
		if orgIDNull.Valid {
			v.OrgID = orgIDNull.String
		}
		vars = append(vars, &v)
	}
	return vars, rows.Err()
}

// DeleteVariable deletes a workflow variable.
func (db *DB) DeleteVariable(id string) error {
	_, err := db.exec("DELETE FROM workflow_variables WHERE id = ?", id)
	return err
}

// ----- Runners -----

// CreateRunner creates a new runner.
func (db *DB) CreateRunner(name, orgID string, labels []string) (*model.Runner, error) {
	id := newUUID()
	now := time.Now().Unix()
	labelsJSON, _ := json.Marshal(labels)

	var orgIDPtr interface{}
	if orgID != "" {
		orgIDPtr = orgID
	}

	_, err := db.exec(
		"INSERT INTO runners (id, name, org_id, labels, status, created_at) VALUES (?, ?, ?, ?, ?, ?)",
		id, name, orgIDPtr, string(labelsJSON), model.RunnerStatusOffline, now,
	)
	if err != nil {
		return nil, err
	}
	return db.GetRunnerByID(id)
}

// GetRunnerByID retrieves a runner by ID.
func (db *DB) GetRunnerByID(id string) (*model.Runner, error) {
	var r model.Runner
	var labelsJSON string
	var createdAt int64
	var lastSeenAt sql.NullInt64
	var orgID sql.NullString

	err := db.queryRow(
		"SELECT id, name, org_id, labels, status, last_seen_at, created_at FROM runners WHERE id = ?",
		id,
	).Scan(&r.ID, &r.Name, &orgID, &labelsJSON, &r.Status, &lastSeenAt, &createdAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}

	r.CreatedAt = time.Unix(createdAt, 0)
	if lastSeenAt.Valid {
		r.LastSeenAt = time.Unix(lastSeenAt.Int64, 0)
	}
	if orgID.Valid {
		r.OrgID = orgID.String
	}
	json.Unmarshal([]byte(labelsJSON), &r.Labels)
	return &r, nil
}

// UpdateRunnerStatus updates a runner's status and last seen time.
func (db *DB) UpdateRunnerStatus(id, status string) error {
	now := time.Now().Unix()
	_, err := db.exec("UPDATE runners SET status = ?, last_seen_at = ? WHERE id = ?", status, now, id)
	return err
}

// ListRunners lists all runners, optionally filtered by org.
func (db *DB) ListRunners(orgID string) ([]*model.Runner, error) {
	var rows *sql.Rows
	var err error

	if orgID != "" {
		rows, err = db.query(
			"SELECT id, name, org_id, labels, status, last_seen_at, created_at FROM runners WHERE org_id = ? OR org_id IS NULL ORDER BY name",
			orgID,
		)
	} else {
		rows, err = db.query(
			"SELECT id, name, org_id, labels, status, last_seen_at, created_at FROM runners ORDER BY name",
		)
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var runners []*model.Runner
	for rows.Next() {
		var r model.Runner
		var labelsJSON string
		var createdAt int64
		var lastSeenAt sql.NullInt64
		var orgIDNull sql.NullString

		if err := rows.Scan(&r.ID, &r.Name, &orgIDNull, &labelsJSON, &r.Status, &lastSeenAt, &createdAt); err != nil {
			return nil, err
		}
		r.CreatedAt = time.Unix(createdAt, 0)
		if lastSeenAt.Valid {
			r.LastSeenAt = time.Unix(lastSeenAt.Int64, 0)
		}
		if orgIDNull.Valid {
			r.OrgID = orgIDNull.String
		}
		json.Unmarshal([]byte(labelsJSON), &r.Labels)
		runners = append(runners, &r)
	}
	return runners, rows.Err()
}

// ----- Workflow Cache -----

// GetCache looks up a cache entry by repo and key.
func (db *DB) GetCache(repoID, cacheKey string) (*model.WorkflowCache, error) {
	var c model.WorkflowCache
	var createdAt, lastUsed int64

	err := db.queryRow(
		"SELECT id, repo_id, cache_key, path, size, created_at, last_used FROM workflow_cache WHERE repo_id = ? AND cache_key = ?",
		repoID, cacheKey,
	).Scan(&c.ID, &c.RepoID, &c.CacheKey, &c.Path, &c.Size, &createdAt, &lastUsed)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	c.CreatedAt = time.Unix(createdAt, 0)
	c.LastUsed = time.Unix(lastUsed, 0)
	return &c, nil
}

// GetCacheByPrefix looks up the most recent cache entry matching a key prefix.
func (db *DB) GetCacheByPrefix(repoID, keyPrefix string) (*model.WorkflowCache, error) {
	var c model.WorkflowCache
	var createdAt, lastUsed int64

	err := db.queryRow(
		"SELECT id, repo_id, cache_key, path, size, created_at, last_used FROM workflow_cache WHERE repo_id = ? AND cache_key LIKE ? ORDER BY last_used DESC LIMIT 1",
		repoID, keyPrefix+"%",
	).Scan(&c.ID, &c.RepoID, &c.CacheKey, &c.Path, &c.Size, &createdAt, &lastUsed)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	c.CreatedAt = time.Unix(createdAt, 0)
	c.LastUsed = time.Unix(lastUsed, 0)
	return &c, nil
}

// CreateCache creates or updates a cache entry.
func (db *DB) CreateCache(repoID, cacheKey, path string, size int64) (*model.WorkflowCache, error) {
	id := newUUID()
	now := time.Now().Unix()

	// Upsert: replace if key already exists for this repo
	_, err := db.exec(
		"INSERT INTO workflow_cache (id, repo_id, cache_key, path, size, created_at, last_used) VALUES (?, ?, ?, ?, ?, ?, ?) ON CONFLICT (repo_id, cache_key) DO UPDATE SET path = ?, size = ?, last_used = ?",
		id, repoID, cacheKey, path, size, now, now, path, size, now,
	)
	if err != nil {
		return nil, err
	}
	return db.GetCache(repoID, cacheKey)
}

// TouchCache updates the last_used timestamp for a cache entry.
func (db *DB) TouchCache(id string) error {
	now := time.Now().Unix()
	_, err := db.exec("UPDATE workflow_cache SET last_used = ? WHERE id = ?", now, id)
	return err
}

// DeleteCache deletes a cache entry by ID.
func (db *DB) DeleteCache(id string) error {
	_, err := db.exec("DELETE FROM workflow_cache WHERE id = ?", id)
	return err
}

// DeleteExpiredCaches deletes cache entries not used since the given time.
func (db *DB) DeleteExpiredCaches(notUsedSince time.Time) ([]model.WorkflowCache, error) {
	threshold := notUsedSince.Unix()
	rows, err := db.query(
		"SELECT id, repo_id, cache_key, path, size, created_at, last_used FROM workflow_cache WHERE last_used < ?",
		threshold,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var expired []model.WorkflowCache
	for rows.Next() {
		var c model.WorkflowCache
		var createdAt, lastUsed int64
		if err := rows.Scan(&c.ID, &c.RepoID, &c.CacheKey, &c.Path, &c.Size, &createdAt, &lastUsed); err != nil {
			return nil, err
		}
		c.CreatedAt = time.Unix(createdAt, 0)
		c.LastUsed = time.Unix(lastUsed, 0)
		expired = append(expired, c)
	}

	if len(expired) > 0 {
		_, err = db.exec("DELETE FROM workflow_cache WHERE last_used < ?", threshold)
		if err != nil {
			return nil, err
		}
	}
	return expired, rows.Err()
}

// DeleteArtifact deletes an artifact by ID.
func (db *DB) DeleteArtifact(id string) error {
	_, err := db.exec("DELETE FROM artifacts WHERE id = ?", id)
	return err
}

// DeleteExpiredArtifacts deletes artifacts past their expiration time. Returns deleted artifacts for storage cleanup.
func (db *DB) DeleteExpiredArtifacts() ([]model.Artifact, error) {
	now := time.Now().Unix()
	rows, err := db.query(
		"SELECT id, workflow_run_id, job_id, name, path, size, expires_at, created_at FROM artifacts WHERE expires_at IS NOT NULL AND expires_at < ?",
		now,
	)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	var expired []model.Artifact
	for rows.Next() {
		var a model.Artifact
		var createdAt int64
		var expiresAt sql.NullInt64
		if err := rows.Scan(&a.ID, &a.WorkflowRunID, &a.JobID, &a.Name, &a.Path, &a.Size, &expiresAt, &createdAt); err != nil {
			return nil, err
		}
		a.CreatedAt = time.Unix(createdAt, 0)
		if expiresAt.Valid {
			a.ExpiresAt = time.Unix(expiresAt.Int64, 0)
		}
		expired = append(expired, a)
	}

	if len(expired) > 0 {
		_, err = db.exec("DELETE FROM artifacts WHERE expires_at IS NOT NULL AND expires_at < ?", now)
		if err != nil {
			return nil, err
		}
	}
	return expired, rows.Err()
}

// GetArtifactByName retrieves an artifact by workflow run ID and name.
func (db *DB) GetArtifactByName(workflowRunID, name string) (*model.Artifact, error) {
	var a model.Artifact
	var createdAt int64
	var expiresAt sql.NullInt64

	err := db.queryRow(
		"SELECT id, workflow_run_id, job_id, name, path, size, expires_at, created_at FROM artifacts WHERE workflow_run_id = ? AND name = ?",
		workflowRunID, name,
	).Scan(&a.ID, &a.WorkflowRunID, &a.JobID, &a.Name, &a.Path, &a.Size, &expiresAt, &createdAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	a.CreatedAt = time.Unix(createdAt, 0)
	if expiresAt.Valid {
		a.ExpiresAt = time.Unix(expiresAt.Int64, 0)
	}
	return &a, nil
}

// ----- Concurrency Locks -----

// AcquireConcurrencyLock tries to acquire a lock for a concurrency group.
// Returns the lock if acquired, or the existing lock if the group is already locked.
func (db *DB) AcquireConcurrencyLock(groupKey, workflowRunID, repoID string, jobID string) (*model.ConcurrencyLock, bool, error) {
	id := newUUID()
	now := time.Now().Unix()

	var jobPtr interface{}
	if jobID != "" {
		jobPtr = jobID
	}

	_, err := db.exec(
		"INSERT INTO concurrency_locks (id, group_key, workflow_run_id, job_id, repo_id, created_at) VALUES (?, ?, ?, ?, ?, ?)",
		id, groupKey, workflowRunID, jobPtr, repoID, now,
	)
	if err != nil {
		// Likely unique constraint violation — group already locked
		existing, getErr := db.GetConcurrencyLock(groupKey)
		if getErr != nil {
			return nil, false, err
		}
		return existing, false, nil
	}

	lock := &model.ConcurrencyLock{
		ID:            id,
		GroupKey:      groupKey,
		WorkflowRunID: workflowRunID,
		JobID:         jobID,
		RepoID:        repoID,
		CreatedAt:     time.Unix(now, 0),
	}
	return lock, true, nil
}

// GetConcurrencyLock retrieves the active lock for a concurrency group.
func (db *DB) GetConcurrencyLock(groupKey string) (*model.ConcurrencyLock, error) {
	var l model.ConcurrencyLock
	var createdAt int64
	var jobID sql.NullString

	err := db.queryRow(
		"SELECT id, group_key, workflow_run_id, job_id, repo_id, created_at FROM concurrency_locks WHERE group_key = ?",
		groupKey,
	).Scan(&l.ID, &l.GroupKey, &l.WorkflowRunID, &jobID, &l.RepoID, &createdAt)
	if err == sql.ErrNoRows {
		return nil, ErrNotFound
	}
	if err != nil {
		return nil, err
	}
	l.CreatedAt = time.Unix(createdAt, 0)
	if jobID.Valid {
		l.JobID = jobID.String
	}
	return &l, nil
}

// ReleaseConcurrencyLock releases a lock by workflow run ID.
func (db *DB) ReleaseConcurrencyLock(workflowRunID string) error {
	_, err := db.exec("DELETE FROM concurrency_locks WHERE workflow_run_id = ?", workflowRunID)
	return err
}

// ReleaseConcurrencyLockByGroup releases a lock by group key.
func (db *DB) ReleaseConcurrencyLockByGroup(groupKey string) error {
	_, err := db.exec("DELETE FROM concurrency_locks WHERE group_key = ?", groupKey)
	return err
}

// ReleaseStaleLocksForRepo releases locks for runs that are already completed.
func (db *DB) ReleaseStaleLocksForRepo(repoID string) (int64, error) {
	result, err := db.exec(
		"DELETE FROM concurrency_locks WHERE repo_id = ? AND workflow_run_id IN (SELECT id FROM workflow_runs WHERE status = ?)",
		repoID, model.RunStatusCompleted,
	)
	if err != nil {
		return 0, err
	}
	n, _ := result.RowsAffected()
	return n, nil
}
