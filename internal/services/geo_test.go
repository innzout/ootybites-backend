package services

import (
	"math"
	"testing"

	"github.com/innzout/ootybites/internal/models"
)

func TestHaversineKm(t *testing.T) {
	cases := []struct {
		name                   string
		lat1, lng1, lat2, lng2 float64
		want, tolerance        float64
	}{
		{"same point", 11.41, 76.70, 11.41, 76.70, 0, 0.001},
		// Ooty to Coonoor: ~13 km in a straight line (longer by road).
		{"Ooty to Coonoor", 11.4102, 76.6950, 11.3530, 76.7959, 12.9, 1.0},
		// Chennai centre to Siruseri: ~29 km in a straight line.
		{"Chennai to Siruseri", 13.0827, 80.2707, 12.8255, 80.2196, 29, 1.5},
		{"symmetric", 12.8255, 80.2196, 13.0827, 80.2707, 29, 1.5},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := HaversineKm(tc.lat1, tc.lng1, tc.lat2, tc.lng2)
			if math.Abs(got-tc.want) > tc.tolerance {
				t.Errorf("HaversineKm = %.2f, want %.2f ± %.1f", got, tc.want, tc.tolerance)
			}
		})
	}
}

func TestValidCoordinate(t *testing.T) {
	cases := []struct {
		lat, lng float64
		want     bool
	}{
		{11.41, 76.70, true},
		{90, 180, true},
		{-90, -180, true},
		{91, 0, false},
		{0, 181, false},
		{math.NaN(), 76.7, false},
	}
	for _, tc := range cases {
		if got := ValidCoordinate(tc.lat, tc.lng); got != tc.want {
			t.Errorf("ValidCoordinate(%v, %v) = %v, want %v", tc.lat, tc.lng, got, tc.want)
		}
	}
}

func TestRankByDistance(t *testing.T) {
	area := func(name string) models.Area { return models.Area{ID: name, Name: name} }
	in := []areaWithHub{
		{area: area("Far"), hubLat: 13.0827, hubLng: 80.2707},    // Chennai
		{area: area("Near-B"), hubLat: 11.4102, hubLng: 76.6950}, // Ooty hub
		{area: area("Near-A"), hubLat: 11.4102, hubLng: 76.6950}, // same Ooty hub
		{area: area("Middle"), hubLat: 11.3530, hubLng: 76.7959}, // Coonoor
	}
	// Shopper standing in Ooty.
	got := rankByDistance(in, 11.4102, 76.6950)

	order := []string{got[0].Name, got[1].Name, got[2].Name, got[3].Name}
	want := []string{"Near-A", "Near-B", "Middle", "Far"}
	for i := range want {
		if order[i] != want[i] {
			t.Fatalf("order = %v, want %v", order, want)
		}
	}
	if !got[0].InRange || !got[2].InRange {
		t.Error("hubs within the radius should be in range")
	}
	if got[3].InRange {
		t.Errorf("Chennai (%.0f km away) must be out of range", got[3].DistanceKm)
	}
}
