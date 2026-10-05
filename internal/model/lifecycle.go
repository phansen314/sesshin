package model

// LifecycleName is lifecycle.json's name in a session directory.
const LifecycleName = "lifecycle.json"

// The statuses the hooks set (the field is an open set; design-spec.md,
// lifecycle.json).
const (
	StatusIdle          = "idle"
	StatusWorking       = "working"
	StatusWaiting       = "waiting"
	StatusNeedsApproval = "needs_approval"
)

// LifecycleFile is a session's lifecycle.json (design-spec.md,
// lifecycle.json). Its fields are in the schema's order, which is the order
// they are written in (File format). A pointer field is nullable: nil is
// null.
type LifecycleFile struct {
	Schema          LifecycleVersion `json:"schema"`
	SessionID       string           `json:"session_id"`
	Cwd             *string          `json:"cwd"`
	TranscriptPath  *string          `json:"transcript_path"`
	SessionTitle    *string          `json:"session_title"`
	Model           *string          `json:"model"`
	PermissionMode  *string          `json:"permission_mode"`
	PID             *int64           `json:"pid"`
	PIDStartedAt    *string          `json:"pid_started_at"`
	Entrypoint      *string          `json:"entrypoint"`
	Nested          *bool            `json:"nested"`
	StartedAt       Timestamp        `json:"started_at"`
	LastStartAt     Timestamp        `json:"last_start_at"`
	Status          string           `json:"status"`
	Compactions     int64            `json:"compactions"`
	StallReason     *string          `json:"stall_reason"`
	BackgroundTasks *int64           `json:"background_tasks"`
	SessionCrons    *int64           `json:"session_crons"`
	LastEventType   string           `json:"last_event_type"`
	LastEventAt     Timestamp        `json:"last_event_at"`
	EventSeq        int64            `json:"event_seq"`
	EndedPromptID   *string          `json:"ended_prompt_id"`
	EndedAt         *Timestamp       `json:"ended_at"`
	EndReason       *string          `json:"end_reason"`
}

// ReadLifecycle reads the lifecycle.json in the session directory named dir.
// Beyond the schema, its session_id must be dir, pid_started_at must be null
// exactly when pid is, and end_reason may be set only while ended_at is
// (design-spec.md, lifecycle.json; hooks-spec.md, session-end). Its content
// is meaningful only when the result is usable.
func ReadLifecycle(data []byte, dir string) (LifecycleFile, FileResult) {
	return readFile(data, LifecycleSchema, func(l *LifecycleFile, f *Fields, p *Problems) {
		if v, ok := f.Required("session_id"); ok {
			if s, ok := p.guarded(v, f.Ptr("session_id"), IsUUID, reasonUUID); ok {
				l.SessionID = s
				if s != dir {
					p.AddAdditional(f.Ptr("session_id"), "must be its directory's name, "+dir)
				}
			}
		}
		l.Cwd = nullableText(f, p, "cwd")
		l.TranscriptPath = nullableText(f, p, "transcript_path")
		l.SessionTitle = nullableText(f, p, "session_title")
		l.Model = nullableText(f, p, "model")
		l.PermissionMode = nullableGuarded(f, p, "permission_mode", IsPermissionMode, reasonPermissionMode)
		pid, pidOK := nullablePositive(f, p, "pid")
		started, startedOK := nullableStartedAt(f, p, "pid_started_at")
		l.PID, l.PIDStartedAt = pid, started
		l.Entrypoint = nullableGuarded(f, p, "entrypoint", IsEntrypoint, reasonEntrypoint)
		if v, ok := f.Required("nested"); ok {
			l.Nested, _ = nullable(p, v, f.Ptr("nested"), (*Problems).Bool)
		}
		l.StartedAt = timestamp(f, p, "started_at")
		l.LastStartAt = timestamp(f, p, "last_start_at")
		if v, ok := f.Required("status"); ok {
			l.Status, _ = p.guarded(v, f.Ptr("status"), IsEnum, reasonEnum)
		}
		if v, ok := f.Required("compactions"); ok {
			l.Compactions, _ = p.Int(v, f.Ptr("compactions"), 0, MaxSafe)
		}
		l.StallReason = nullableGuarded(f, p, "stall_reason", IsEnum, reasonEnum)
		l.BackgroundTasks = nullableCount(f, p, "background_tasks")
		l.SessionCrons = nullableCount(f, p, "session_crons")
		if v, ok := f.Required("last_event_type"); ok {
			l.LastEventType, _ = p.guarded(v, f.Ptr("last_event_type"), IsEventType, reasonEventType)
		}
		l.LastEventAt = timestamp(f, p, "last_event_at")
		if v, ok := f.Required("event_seq"); ok {
			l.EventSeq, _ = p.Int(v, f.Ptr("event_seq"), 1, MaxSafe)
		}
		l.EndedPromptID = nullableText(f, p, "ended_prompt_id")
		endedOK := false
		if v, ok := f.Required("ended_at"); ok {
			l.EndedAt, endedOK = nullable(p, v, f.Ptr("ended_at"), (*Problems).Timestamp)
		}
		reasonOK := false
		if v, ok := f.Required("end_reason"); ok {
			l.EndReason, reasonOK = nullable(p, v, f.Ptr("end_reason"), guardedBy(IsEnum, reasonEnum))
		}

		pidPair(p, f, pid, pidOK, started, startedOK)
		if endedOK && reasonOK && l.EndedAt == nil && l.EndReason != nil {
			p.AddAdditional(f.Ptr("end_reason"), "must be null while ended_at is")
		}
	})
}

// pidPair checks the rule beyond the schema that pid_started_at is null
// exactly when pid is, when both passed their own checks.
func pidPair(p *Problems, f *Fields, pid *int64, pidOK bool, started *string, startedOK bool) {
	if pidOK && startedOK && (pid == nil) != (started == nil) {
		p.AddAdditional(f.Ptr("pid_started_at"), "must be null exactly when pid is")
	}
}

// The field helpers below check the required member key of f, which may be
// null, and return its value, nil when it is null or fails; a second result
// reports whether it was present and passed.

func nullableText(f *Fields, p *Problems, key string) *string {
	if v, ok := f.Required(key); ok {
		s, _ := nullable(p, v, f.Ptr(key), (*Problems).text)
		return s
	}
	return nil
}

func nullableGuarded(f *Fields, p *Problems, key string, is func(string) bool, reason string) *string {
	if v, ok := f.Required(key); ok {
		s, _ := nullable(p, v, f.Ptr(key), guardedBy(is, reason))
		return s
	}
	return nil
}

func nullablePositive(f *Fields, p *Problems, key string) (*int64, bool) {
	if v, ok := f.Required(key); ok {
		return nullable(p, v, f.Ptr(key), func(p *Problems, v any, ptr string) (int64, bool) { return p.Int(v, ptr, 1, MaxSafe) })
	}
	return nil, false
}

func nullableStartedAt(f *Fields, p *Problems, key string) (*string, bool) {
	if v, ok := f.Required(key); ok {
		return nullable(p, v, f.Ptr(key), (*Problems).pidStartedAt)
	}
	return nil, false
}

func nullableCount(f *Fields, p *Problems, key string) *int64 {
	if v, ok := f.Required(key); ok {
		n, _ := nullable(p, v, f.Ptr(key), func(p *Problems, v any, ptr string) (int64, bool) { return p.Int(v, ptr, 0, MaxSafe) })
		return n
	}
	return nil
}

func timestamp(f *Fields, p *Problems, key string) Timestamp {
	if v, ok := f.Required(key); ok {
		ts, _ := p.Timestamp(v, f.Ptr(key))
		return ts
	}
	return ""
}

// guardedBy returns a check of a string passing is.
func guardedBy(is func(string) bool, reason string) func(*Problems, any, string) (string, bool) {
	return func(p *Problems, v any, ptr string) (string, bool) { return p.guarded(v, ptr, is, reason) }
}
