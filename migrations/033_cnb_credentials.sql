-- Personal CNB (cnb.cool) access token. Apply manually; startup does not run
-- this script. gorm AutoMigrate also adds the column on process start.
-- Required token scopes: repo-code:rw and repo-pr:rw.

ALTER TABLE user_configs ADD COLUMN cnb_token TEXT NULL;
