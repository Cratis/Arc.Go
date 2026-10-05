// Copyright (c) Cratis. All rights reserved.
// Licensed under the MIT license. See LICENSE file in the project root for full license information.

package queries

import "slices"

// ObservationHealth aggregates currently owned observations for one bounded query
// label. Counts overlap host hub operations; they are not physical connections.
// Delivered is the acknowledged data count of these owners, not historical traffic.
// Snapshots are detached and are not globally atomic across owners.
type ObservationHealth struct {
	// Query is a registered label or _other, never an untrusted lookup name.
	Query string `json:"query"`
	// Opening counts tracked source/resource admission operations.
	Opening int `json:"opening"`
	// Running counts opened owners that are not closing, including idle consumers.
	Running int `json:"running"`
	// Closing counts owners with an in-progress close, excluding retained owners.
	Closing int `json:"closing"`
	// Retained counts owners whose cleanup has not confirmed a join.
	Retained int `json:"retained"`
	// Delivered counts successfully acknowledged data results for these owners.
	Delivered uint64 `json:"delivered"`
}

// HealthReporter is an optional read-only native-owner capability. First-party
// pipelines return no state unless PipelineOptions.EnableQueryHealth is enabled.
// Hosts must authorize access before calling it; it does not expose identifiers,
// principals, input, cached results, or exception details.
type HealthReporter interface {
	QueryHealth() []ObservationHealth
}

func (p *queryPipeline) QueryHealth() []ObservationHealth {
	if p.healthLabels == nil {
		return nil
	}
	p.observationMu.Lock()
	owners := make([]*Observation, 0, len(p.observations))
	for owner := range p.observations {
		owners = append(owners, owner)
	}
	p.observationMu.Unlock()
	groups := make(map[string]ObservationHealth)
	for _, owner := range owners {
		label := p.healthLabels.Label(string(owner.query.descriptor.Identity()))
		group := groups[label]
		group.Query = label
		owner.mu.Lock()
		if !owner.closed {
			switch {
			case owner.retained:
				group.Retained++
			case owner.closing:
				group.Closing++
			case owner.opening:
				group.Opening++
			default:
				group.Running++
			}
			group.Delivered += owner.delivered
			groups[label] = group
		}
		owner.mu.Unlock()
	}
	result := make([]ObservationHealth, 0, len(groups))
	for _, group := range groups {
		result = append(result, group)
	}
	slices.SortFunc(result, func(a, b ObservationHealth) int {
		if a.Query < b.Query {
			return -1
		}
		if a.Query > b.Query {
			return 1
		}
		return 0
	})
	return result
}
