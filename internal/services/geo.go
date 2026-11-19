package services

import (
	"context"
	"fmt"
	"math"
	"sort"

	"github.com/innzout/ootybites/internal/models"
)

// ExpressRadiusKm is how far from a hub 24-hour delivery is offered. Beyond it
// the nearest area is still reported, but marked out of range, so the shopper
// is told plainly that express does not reach them rather than being shown a
// hub's stock it cannot deliver.
const ExpressRadiusKm = 25.0

const earthRadiusKm = 6371.0

// HaversineKm is the great-circle distance between two points in kilometres.
// Straight-line, not road distance: good enough to rank hubs and apply a
// radius, and it needs no external API.
func HaversineKm(lat1, lng1, lat2, lng2 float64) float64 {
	rad := func(d float64) float64 { return d * math.Pi / 180 }
	dLat, dLng := rad(lat2-lat1), rad(lng2-lng1)
	a := math.Sin(dLat/2)*math.Sin(dLat/2) +
		math.Cos(rad(lat1))*math.Cos(rad(lat2))*math.Sin(dLng/2)*math.Sin(dLng/2)
	return 2 * earthRadiusKm * math.Asin(math.Min(1, math.Sqrt(a)))
}

// ValidCoordinate reports whether lat/lng are a real point on Earth. NaN and
// out-of-range values are rejected rather than producing nonsense distances.
func ValidCoordinate(lat, lng float64) bool {
	return !math.IsNaN(lat) && !math.IsNaN(lng) && lat >= -90 && lat <= 90 && lng >= -180 && lng <= 180
}

// NearbyArea is a serviceable area with its hub's distance from the shopper.
type NearbyArea struct {
	models.Area
	DistanceKm float64 `json:"distance_km"`
	InRange    bool    `json:"in_range"`
}

// areaWithHub carries the hub pin alongside an area for ranking.
type areaWithHub struct {
	area           models.Area
	hubLat, hubLng float64
}

// rankByDistance orders areas by their hub's distance from (lat, lng). Ties
// (several areas served by one hub) keep a stable, alphabetical order.
func rankByDistance(in []areaWithHub, lat, lng float64) []NearbyArea {
	out := make([]NearbyArea, 0, len(in))
	for _, a := range in {
		d := HaversineKm(lat, lng, a.hubLat, a.hubLng)
		out = append(out, NearbyArea{
			Area:       a.area,
			DistanceKm: math.Round(d*10) / 10,
			InRange:    d <= ExpressRadiusKm,
		})
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].DistanceKm != out[j].DistanceKm {
			return out[i].DistanceKm < out[j].DistanceKm
		}
		return out[i].Name < out[j].Name
	})
	return out
}

// NearestServiceable returns active express areas ranked by how close their
// hub is to the given point. Hubs without a map pin cannot be ranked and are
// left out; they are still reachable by choosing the area by hand.
func (s *Areas) NearestServiceable(ctx context.Context, lat, lng float64) ([]NearbyArea, error) {
	rows, err := s.db.Query(ctx,
		`SELECT a.id, a.code, a.name, a.city, a.pincode, a.hub_id, h.name, a.is_active, a.created_at, h.lat, h.lng
		 FROM areas a JOIN hubs h ON h.id = a.hub_id
		 WHERE a.is_active AND h.is_active AND h.lat IS NOT NULL AND h.lng IS NOT NULL`)
	if err != nil {
		return nil, fmt.Errorf("nearest areas: %w", err)
	}
	defer rows.Close()
	var in []areaWithHub
	for rows.Next() {
		var x areaWithHub
		a := &x.area
		if err := rows.Scan(&a.ID, &a.Code, &a.Name, &a.City, &a.Pincode, &a.HubID, &a.HubName,
			&a.IsActive, &a.CreatedAt, &x.hubLat, &x.hubLng); err != nil {
			return nil, err
		}
		in = append(in, x)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	return rankByDistance(in, lat, lng), nil
}
