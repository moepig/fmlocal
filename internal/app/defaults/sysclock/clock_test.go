package sysclock_test

import (
	"testing"
	"time"

	"github.com/moepig/fmlocal/internal/app/defaults/sysclock"
	"github.com/stretchr/testify/assert"
)

func TestSystemClockReturnsCurrentTime(t *testing.T) {
	c := sysclock.System{}
	before := time.Now()
	got := c.Now()
	after := time.Now()
	assert.False(t, got.Before(before))
	assert.False(t, got.After(after))
}

func TestFake(t *testing.T) {
	anchor := time.Unix(1700000000, 0).UTC()
	c := sysclock.NewFake(anchor)
	assert.Equal(t, anchor, c.Now())
	c.Advance(5 * time.Second)
	assert.Equal(t, anchor.Add(5*time.Second), c.Now())
	target := time.Unix(42, 0).UTC()
	c.Set(target)
	assert.Equal(t, target, c.Now())
}
