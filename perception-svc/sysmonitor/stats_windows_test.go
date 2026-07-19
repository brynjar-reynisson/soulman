//go:build windows

package sysmonitor

import (
	"errors"
	"testing"
	"time"
)

func TestWinStats_DiskUsagePercent_ReturnsPlausibleValue(t *testing.T) {
	s := &winStats{}
	pct, err := s.DiskUsagePercent(`C:\`)
	if err != nil {
		t.Fatalf("DiskUsagePercent: %v", err)
	}
	if pct < 0 || pct > 100 {
		t.Errorf("DiskUsagePercent = %v, want value in [0,100]", pct)
	}
}

func TestWinStats_DiskUsagePercent_InvalidPath_ReturnsError(t *testing.T) {
	s := &winStats{}
	if _, err := s.DiskUsagePercent(`Z:\does-not-exist-drive`); err == nil {
		t.Fatal("DiskUsagePercent: want error for a nonexistent drive, got nil")
	}
}

func TestWinStats_MemoryUsagePercent_ReturnsPlausibleValue(t *testing.T) {
	s := &winStats{}
	pct, err := s.MemoryUsagePercent()
	if err != nil {
		t.Fatalf("MemoryUsagePercent: %v", err)
	}
	if pct < 0 || pct > 100 {
		t.Errorf("MemoryUsagePercent = %v, want value in [0,100]", pct)
	}
}

func TestWinStats_CPUUsagePercent_FirstCallReturnsNoBaselineError(t *testing.T) {
	s := &winStats{}
	if _, err := s.CPUUsagePercent(); !errors.Is(err, errNoCPUBaseline) {
		t.Fatalf("CPUUsagePercent first call error = %v, want errNoCPUBaseline", err)
	}
}

func TestWinStats_CPUUsagePercent_SecondCallReturnsPlausibleValue(t *testing.T) {
	s := &winStats{}
	if _, err := s.CPUUsagePercent(); !errors.Is(err, errNoCPUBaseline) {
		t.Fatalf("first call error = %v, want errNoCPUBaseline", err)
	}
	time.Sleep(50 * time.Millisecond)
	pct, err := s.CPUUsagePercent()
	if err != nil {
		t.Fatalf("CPUUsagePercent second call: %v", err)
	}
	if pct < 0 || pct > 100 {
		t.Errorf("CPUUsagePercent = %v, want value in [0,100]", pct)
	}
}
