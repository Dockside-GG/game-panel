package httpapi

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestNormalizeScheduleRequestPreservesBackupRetention(t *testing.T) {
	t.Parallel()
	input := createScheduleRequest{
		Name: "  Nightly backup  ", CronExpression: " 0 4 * * * ", Timezone: " UTC ", Enabled: true,
		Tasks: []scheduleTaskRequest{{
			TaskType: " BACKUP ", TimeoutSeconds: 300,
			Config: json.RawMessage(`{"name":"World","include_paths":["Saved"],"exclude_globs":["logs/*"],"retention_days":7}`),
		}},
	}
	tasks, err := normalizeScheduleRequest(&input)
	if err != nil {
		t.Fatalf("normalizeScheduleRequest() error = %v", err)
	}
	if input.Name != "Nightly backup" || input.CronExpression != "0 4 * * *" || input.Timezone != "UTC" {
		t.Fatalf("request was not normalized: %#v", input)
	}
	if len(tasks) != 1 || tasks[0].TaskType != "backup" || !strings.Contains(string(tasks[0].Config), `"retention_days":7`) {
		t.Fatalf("tasks = %#v", tasks)
	}
}

func TestNormalizeScheduleRequestRejectsInvalidBackupRetention(t *testing.T) {
	t.Parallel()
	input := createScheduleRequest{
		Name: "Backup", CronExpression: "0 4 * * *", Timezone: "UTC", Enabled: true,
		Tasks: []scheduleTaskRequest{{
			TaskType: "backup",
			Config:   json.RawMessage(`{"name":"World","include_paths":[],"exclude_globs":[],"retention_days":3651}`),
		}},
	}
	if _, err := normalizeScheduleRequest(&input); err == nil {
		t.Fatal("normalizeScheduleRequest() accepted invalid retention")
	}
}
