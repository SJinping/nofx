package trader

import "testing"

func TestFormatPriceToTickSize(t *testing.T) {
	tests := []struct {
		name      string
		price     float64
		tickSize  float64
		precision int
		want      string
	}{
		{name: "eth two decimals", price: 1928.9135, tickSize: 0.01, precision: 2, want: "1928.91"},
		{name: "integer tick", price: 65321.49, tickSize: 0.1, precision: 1, want: "65321.5"},
		{name: "non decimal-power tick", price: 10.37, tickSize: 0.25, precision: 2, want: "10.25"},
		{name: "zero precision", price: 123.6, tickSize: 1, precision: 0, want: "124"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := formatPriceToTickSize(tt.price, tt.tickSize, tt.precision)
			if err != nil {
				t.Fatalf("formatPriceToTickSize returned error: %v", err)
			}
			if got != tt.want {
				t.Fatalf("formatPriceToTickSize() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestFormatPriceToTickSizeRejectsInvalidInputs(t *testing.T) {
	if _, err := formatPriceToTickSize(0, 0.01, 2); err == nil {
		t.Fatal("expected error for non-positive price")
	}
	if _, err := formatPriceToTickSize(1, 0, 2); err == nil {
		t.Fatal("expected error for non-positive tickSize")
	}
}
