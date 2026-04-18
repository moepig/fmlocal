package idgen_test

import (
	"testing"

	"github.com/moepig/fmlocal/internal/app/defaults/idgen"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestUUID(t *testing.T) {
	g := idgen.NewUUID()
	a := g.NewID()
	b := g.NewID()
	require.NotEmpty(t, a)
	assert.NotEqual(t, a, b)
}

func TestSequence(t *testing.T) {
	g := idgen.NewSequence("t-")
	assert.Equal(t, "t-1", g.NewID())
	assert.Equal(t, "t-2", g.NewID())
}
