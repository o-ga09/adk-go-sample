package llmusage

import (
	"context"
	"testing"
)

func TestTriggerFromContext(t *testing.T) {
	tests := []struct {
		name string
		ctx  func() context.Context
		want Trigger
	}{
		{
			name: "タグ未設定はTriggerAPIにフォールバック",
			ctx:  func() context.Context { return context.Background() },
			want: TriggerAPI,
		},
		{
			name: "WithTriggerで設定した値を読み出せる",
			ctx:  func() context.Context { return WithTrigger(context.Background(), TriggerBatch) },
			want: TriggerBatch,
		},
		{
			name: "Slackトリガーも読み出せる",
			ctx:  func() context.Context { return WithTrigger(context.Background(), TriggerSlack) },
			want: TriggerSlack,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := TriggerFromContext(tt.ctx()); got != tt.want {
				t.Errorf("TriggerFromContext() = %q, want %q", got, tt.want)
			}
		})
	}
}
