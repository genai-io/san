package kit

import "testing"

func TestLiveTaskDotLightContrast(t *testing.T) {
	var dim, peak float64
	for frame, color := range taskDotBreath {
		contrast := contrastRatio(color.Light, CurrentTheme.Background.Light)
		if contrast < 3 {
			t.Errorf("frame %d: contrast %.2f:1 is below 3:1", frame, contrast)
		}
		if frame == 0 {
			dim = contrast
		}
		peak = max(peak, contrast)
	}
	if peak-dim < 2 {
		t.Errorf("contrast should vary noticeably across a breath: dim %.2f, peak %.2f", dim, peak)
	}
}
