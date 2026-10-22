package core

import "sort"

// Join groups found secrets by value and attaches the matching provider key.
// Provider keys with no local match are returned as keys with no locations
// (finding F3: active but found nowhere).
func Join(found []FoundSecret, remote []ProviderKey) []Key {
	byFP := map[string]*Key{}
	var order []string
	for _, f := range found {
		fp := f.Fingerprint()
		k, ok := byFP[fp]
		if !ok {
			k = &Key{ID: Last4(f.Value), Provider: f.Provider, Redacted: Redact(f.Value)}
			for i := range remote {
				if remote[i].Provider == f.Provider && MatchesHint(f.Value, remote[i].Hint) {
					k.Remote = &remote[i]
				}
			}
			byFP[fp] = k
			order = append(order, fp)
		}
		k.Locations = append(k.Locations, f.Location)
	}

	matched := map[string]bool{}
	var keys []Key
	for _, fp := range order {
		k := byFP[fp]
		if k.Remote != nil {
			matched[k.Remote.ID] = true
		}
		k.Label = labelFor(*k)
		keys = append(keys, *k)
	}
	for i := range remote {
		r := remote[i]
		if matched[r.ID] || !r.Active {
			continue
		}
		keys = append(keys, Key{
			ID: Last4(r.Hint), Label: r.Name, Provider: r.Provider,
			Redacted: r.Hint, Remote: &r,
		})
	}
	sort.SliceStable(keys, func(i, j int) bool { return len(keys[i].Locations) > len(keys[j].Locations) })
	return keys
}

// labelFor names a key after the first deployed place it lives, falling back
// to the provider's name for it, then the redacted value.
func labelFor(k Key) string {
	for _, l := range k.Locations {
		if l.Source != "local" && l.Where != "" {
			return l.Where
		}
	}
	if k.Remote != nil && k.Remote.Name != "" {
		return k.Remote.Name
	}
	if len(k.Locations) > 0 {
		return k.Locations[0].Where
	}
	return k.Redacted
}
