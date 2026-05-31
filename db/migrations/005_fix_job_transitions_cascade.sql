-- Migration 005: fix job_transitions FK to use ON DELETE CASCADE
-- job_transitions was the only child table with NO ACTION, blocking job deletion.
ALTER TABLE job_transitions
    DROP CONSTRAINT job_transitions_job_id_fkey,
    ADD  CONSTRAINT job_transitions_job_id_fkey
         FOREIGN KEY (job_id) REFERENCES jobs(id) ON DELETE CASCADE;
