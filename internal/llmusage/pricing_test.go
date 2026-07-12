package llmusage

import "testing"

func TestLoadPricing(t *testing.T) {
	tests := []struct {
		name    string
		jsonStr string
		wantErr bool
		check   func(t *testing.T, table PricingTable)
	}{
		{
			name: "空文字列はデフォルトのgemini-2.5-flash単価を返す",
			check: func(t *testing.T, table PricingTable) {
				p, ok := table["gemini-2.5-flash"]
				if !ok {
					t.Fatal("gemini-2.5-flash missing from default pricing")
				}
				if p.InputPerMillionUSD != 0.30 {
					t.Errorf("InputPerMillionUSD = %v, want 0.30", p.InputPerMillionUSD)
				}
			},
		},
		{
			name:    "既存モデルの単価を上書きできる",
			jsonStr: `{"gemini-2.5-flash":{"inputPerMillionUSD":1,"outputPerMillionUSD":2,"cachedInputPerMillionUSD":0.5}}`,
			check: func(t *testing.T, table PricingTable) {
				p := table["gemini-2.5-flash"]
				if p.InputPerMillionUSD != 1 || p.OutputPerMillionUSD != 2 || p.CachedInputPerMillionUSD != 0.5 {
					t.Errorf("got %+v, want overridden pricing", p)
				}
			},
		},
		{
			name:    "未知のモデルを追加できる",
			jsonStr: `{"gemini-3.0-pro":{"inputPerMillionUSD":5,"outputPerMillionUSD":15}}`,
			check: func(t *testing.T, table PricingTable) {
				if _, ok := table["gemini-2.5-flash"]; !ok {
					t.Error("default pricing entries must survive an additive override")
				}
				if _, ok := table["gemini-3.0-pro"]; !ok {
					t.Error("new model missing from merged table")
				}
			},
		},
		{
			name:    "不正なJSONはエラー",
			jsonStr: `not json`,
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			table, err := LoadPricing(tt.jsonStr)
			if tt.wantErr {
				if err == nil {
					t.Fatal("want error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("LoadPricing: %v", err)
			}
			tt.check(t, table)
		})
	}
}

func TestPricingTable_EstimateCostUSD(t *testing.T) {
	table := PricingTable{
		"test-model": {
			InputPerMillionUSD:       1_000_000, // $1 per token, to keep the numbers simple
			OutputPerMillionUSD:      2_000_000, // $2 per token
			CachedInputPerMillionUSD: 500_000,   // $0.5 per token
		},
	}

	tests := []struct {
		name             string
		model            string
		promptTokens     int32
		candidatesTokens int32
		cachedTokens     int32
		want             float64
	}{
		{
			name:             "キャッシュなし: プロンプト全額を入力単価で課金",
			model:            "test-model",
			promptTokens:     10,
			candidatesTokens: 5,
			want:             10*1 + 5*2,
		},
		{
			name:             "キャッシュあり: 非キャッシュ分のみ入力単価、キャッシュ分はキャッシュ単価",
			model:            "test-model",
			promptTokens:     10,
			candidatesTokens: 5,
			cachedTokens:     4,
			want:             6*1 + 4*0.5 + 5*2,
		},
		{
			name:             "未知のモデルは$0",
			model:            "unknown-model",
			promptTokens:     1000,
			candidatesTokens: 1000,
			want:             0,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := table.EstimateCostUSD(tt.model, tt.promptTokens, tt.candidatesTokens, tt.cachedTokens)
			if got != tt.want {
				t.Errorf("EstimateCostUSD() = %v, want %v", got, tt.want)
			}
		})
	}
}
