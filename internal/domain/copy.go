package domain

// This file owns the in-memory copy boundaries for the state entities.
// These copies are the mutation fence used by the repository: callers work on
// a copy and must never share a mutable map, slice or pointer with the
// committed state. They are deliberately independent of JSON encoding, which
// is the persistence-format concern of the repository disk layer. When a new
// nested mutable field is added to an entity, its clone must be extended here.

// cloneStringMap returns an independent copy of a string map. A nil map stays
// nil so that a missing collection is not turned into an empty one.
func cloneStringMap(values map[string]string) map[string]string {
	if values == nil {
		return nil
	}
	result := make(map[string]string, len(values))
	for key, value := range values {
		result[key] = value
	}
	return result
}

// Clone returns an independent copy of the catalog: the component index, every
// per-component release collection and every release inside them.
func (c Catalog) Clone() Catalog {
	components := make(map[string]Component, len(c.Components))
	for id, component := range c.Components {
		// Component holds only scalar values, so a value copy is sufficient.
		components[id] = component
	}
	releases := make(map[string]map[string]Release, len(c.Releases))
	for id, versions := range c.Releases {
		copied := make(map[string]Release, len(versions))
		for version, release := range versions {
			copied[version] = release.Clone()
		}
		releases[id] = copied
	}
	return Catalog{
		Revision:   c.Revision,
		Components: components,
		Releases:   releases,
	}
}

// Clone returns an independent copy of a release, including its dependency
// constraint map and its optional withdrawal timestamp.
func (r Release) Clone() Release {
	result := r
	result.Requires = cloneStringMap(r.Requires)
	if r.WithdrawnAt != nil {
		withdrawn := *r.WithdrawnAt
		result.WithdrawnAt = &withdrawn
	}
	return result
}

// Clone returns an independent copy of an environment, covering its root
// requirements and the resolved version set.
func (e Environment) Clone() Environment {
	result := e
	result.Roots = cloneStringMap(e.Roots)
	result.Resolved = cloneStringMap(e.Resolved)
	return result
}

// Clone returns an independent copy of a plan, covering the requested roots,
// the resolved version set and the change list.
func (p Plan) Clone() Plan {
	result := p
	result.Roots = cloneStringMap(p.Roots)
	result.Resolved = cloneStringMap(p.Resolved)
	result.Changes = cloneChanges(p.Changes)
	return result
}

// cloneChanges copies the change backing array. Change currently holds only
// scalar fields; a nested field added to Change must be deep-copied here.
func cloneChanges(changes []Change) []Change {
	if changes == nil {
		return nil
	}
	result := make([]Change, len(changes))
	copy(result, changes)
	return result
}
