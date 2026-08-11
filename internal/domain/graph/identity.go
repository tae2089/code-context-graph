package graph

import "cmp"

// Identity is who a node is, told apart from which row it happens to be.
//
// The database gives every node an id in the order the rows were written, so an
// id says when a node was indexed, not what it is. Re-index the same repository
// from a clean checkout and every id can differ while every Identity here stays
// the same. Anything that has to produce the same answer twice — a tie-break, a
// stable sort, a comparison across two databases — belongs on this and not on
// the id.
//
// The four fields are the columns of the node table's uniqueness index
// (namespace, qualified_name, file_path, start_line) plus the kind, so two
// different nodes cannot share one Identity.
// @intent give ranking a key that survives re-indexing, which the node id does not.
type Identity struct {
	FilePath      string
	QualifiedName string
	Kind          NodeKind
	Namespace     string
	StartLine     int
}

// Identity says who this node is.
// @intent read a node's stable identity without repeating which fields make it up.
func (n Node) Identity() Identity {
	return Identity{
		FilePath:      n.FilePath,
		QualifiedName: n.QualifiedName,
		Kind:          n.Kind,
		Namespace:     n.Namespace,
		StartLine:     n.StartLine,
	}
}

// CompareIdentity orders two nodes by who they are, for the callers that have
// run out of reasons to prefer one over the other.
//
// File path comes first so a tie group reads as whole files, matching how an
// evidence list groups it anyway. Namespace is in there because federated search
// can hold the same file in two repositories, and start line because one file
// can declare the same qualified name twice.
// @intent give every layer of search one tie-break, so two layers cannot disagree about who comes first.
func CompareIdentity(a, b Identity) int {
	if by := cmp.Compare(a.FilePath, b.FilePath); by != 0 {
		return by
	}
	if by := cmp.Compare(a.QualifiedName, b.QualifiedName); by != 0 {
		return by
	}
	if by := cmp.Compare(a.Kind, b.Kind); by != 0 {
		return by
	}
	if by := cmp.Compare(a.Namespace, b.Namespace); by != 0 {
		return by
	}
	return cmp.Compare(a.StartLine, b.StartLine)
}
