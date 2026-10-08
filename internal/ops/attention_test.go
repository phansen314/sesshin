package ops

import (
	"testing"

	"github.com/phansen314/sesshin/internal/live"
	"github.com/phansen314/sesshin/internal/model"
)

func TestAttention(t *testing.T) {
	rl := ptrTo("rate_limit")
	none := &Pending{}
	tasks := &Pending{BackgroundTasks: 2}
	crons := &Pending{SessionCrons: 1}
	for _, tc := range []struct {
		liveness string
		status   string
		stall    *string
		pending  *Pending
		want     string // "" is null
	}{
		{"live", model.StatusNeedsApproval, nil, nil, AttentionBlocked},
		{"live", model.StatusNeedsApproval, nil, crons, AttentionBlocked}, // an AskUserQuestion, whatever is pending
		{"live", model.StatusWaiting, rl, nil, AttentionStalled},
		{"live", model.StatusWaiting, rl, tasks, AttentionStalled}, // stalled comes first
		{"live", model.StatusWaiting, nil, tasks, AttentionSelfWaking},
		{"live", model.StatusWaiting, nil, crons, AttentionSelfWaking},
		{"live", model.StatusWaiting, nil, none, AttentionYourTurn},
		{"live", model.StatusWaiting, nil, nil, AttentionYourTurn}, // null is not a count
		{"live", model.StatusIdle, nil, nil, AttentionIdle},
		{"live", model.StatusWorking, nil, nil, AttentionWorking},
		{"live", "compacting", nil, nil, AttentionUnknown},
		{"live", "", nil, nil, AttentionUnknown},
		{"unknown", model.StatusWaiting, nil, nil, AttentionYourTurn},
		{live.Ended.String(), model.StatusWaiting, nil, nil, ""},
		{live.Ended.String(), model.StatusNeedsApproval, nil, nil, ""},
	} {
		got := attention(tc.liveness, tc.status, tc.stall, tc.pending)
		switch {
		case tc.want == "" && got != nil:
			t.Errorf("%+v: %q, want null", tc, *got)
		case tc.want != "" && (got == nil || *got != tc.want):
			t.Errorf("%+v: %v, want %q", tc, got, tc.want)
		}
	}
}
