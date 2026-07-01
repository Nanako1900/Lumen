package auth

// OwnerSet is the immutable set of owner OAuth subjects (contract §5.3).
// Owner status is configuration state (LUMEN_OWNER_SUBJECTS), never persisted;
// membership lookup is O(1).
type OwnerSet struct {
	subs map[string]struct{}
}

// NewOwnerSet builds an OwnerSet from a list of subjects (already trimmed and
// de-emptied by config).
func NewOwnerSet(subjects []string) *OwnerSet {
	m := make(map[string]struct{}, len(subjects))
	for _, s := range subjects {
		if s != "" {
			m[s] = struct{}{}
		}
	}
	return &OwnerSet{subs: m}
}

// IsOwner reports whether sub is an owner.
func (o *OwnerSet) IsOwner(sub string) bool {
	_, ok := o.subs[sub]
	return ok
}
