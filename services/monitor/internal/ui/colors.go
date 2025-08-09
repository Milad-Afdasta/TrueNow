// Package ui provides user interface components for TrueNow Monitor
package ui

import (
	"github.com/gdamore/tcell/v2"
)

// ColorScheme defines colors optimized for black background
type ColorScheme struct {
	// Status Colors
	Healthy   tcell.Color
	Warning   tcell.Color
	Critical  tcell.Color
	Unknown   tcell.Color
	
	// Metric Colors
	Good      tcell.Color  // Good performance
	Normal    tcell.Color  // Normal performance
	Degraded  tcell.Color  // Degraded performance
	Bad       tcell.Color  // Bad performance
	
	// Text Colors
	Primary   tcell.Color  // Main text
	Secondary tcell.Color  // Secondary text
	Muted     tcell.Color  // Less important text
	Highlight tcell.Color  // Important/highlighted text
	
	// UI Elements
	Border    tcell.Color
	Header    tcell.Color
	Label     tcell.Color
	Value     tcell.Color
	
	// Data Visualization
	Data1     tcell.Color  // First data series
	Data2     tcell.Color  // Second data series
	Data3     tcell.Color  // Third data series
	Data4     tcell.Color  // Fourth data series
	
	// Percentile Colors
	P50Color  tcell.Color
	P90Color  tcell.Color
	P95Color  tcell.Color
	P99Color  tcell.Color
	P999Color tcell.Color
}

// DefaultColorScheme returns the default color scheme for black background
// All colors are light/bright to ensure visibility
func DefaultColorScheme() *ColorScheme {
	return &ColorScheme{
		// Status Colors - Bright and clear (no dark red)
		Healthy:   tcell.ColorLightGreen,
		Warning:   tcell.ColorYellow,
		Critical:  tcell.ColorLightPink,  // Changed from Red to LightPink for visibility
		Unknown:   tcell.ColorLightGray,
		
		// Metric Colors - Traffic light pattern with lighter colors
		Good:      tcell.ColorLightGreen,
		Normal:    tcell.ColorLightCyan,
		Degraded:  tcell.ColorYellow,
		Bad:       tcell.ColorLightCoral,  // Changed from OrangeRed to LightCoral
		
		// Text Colors - High contrast whites and grays
		Primary:   tcell.ColorWhite,
		Secondary: tcell.ColorLightGray,
		Muted:     tcell.ColorSilver,
		Highlight: tcell.ColorLightYellow,
		
		// UI Elements - Subtle but visible
		Border:    tcell.ColorLightSlateGray,
		Header:    tcell.ColorLightCyan,
		Label:     tcell.ColorLightBlue,
		Value:     tcell.ColorWhite,
		
		// Data Visualization - Distinct bright colors
		Data1:     tcell.ColorLightCyan,
		Data2:     tcell.ColorLightGreen,
		Data3:     tcell.ColorYellow,
		Data4:     tcell.ColorFuchsia,
		
		// Percentile Colors - Gradient from cool to warm (no dark red)
		P50Color:  tcell.ColorLightCyan,
		P90Color:  tcell.ColorLightGreen,
		P95Color:  tcell.ColorYellow,
		P99Color:  tcell.ColorLightSalmon,  // Changed from Orange to LightSalmon
		P999Color: tcell.ColorHotPink,      // Changed from Red to HotPink
	}
}

// GetColorForPercentage returns appropriate color based on percentage value
func (cs *ColorScheme) GetColorForPercentage(value, goodThreshold, badThreshold float64) tcell.Color {
	if value <= goodThreshold {
		return cs.Good
	} else if value <= badThreshold {
		return cs.Normal
	} else if value <= badThreshold*1.5 {
		return cs.Degraded
	}
	return cs.Bad
}

// GetColorForLatency returns color based on latency in milliseconds
func (cs *ColorScheme) GetColorForLatency(latencyMs float64) tcell.Color {
	switch {
	case latencyMs < 10:
		return cs.Good
	case latencyMs < 50:
		return cs.Normal
	case latencyMs < 100:
		return cs.Degraded
	case latencyMs < 500:
		return cs.Warning
	default:
		return cs.Critical
	}
}

// GetColorForErrorRate returns color based on error rate percentage
func (cs *ColorScheme) GetColorForErrorRate(errorRate float64) tcell.Color {
	switch {
	case errorRate < 0.1:
		return cs.Good
	case errorRate < 1.0:
		return cs.Normal
	case errorRate < 5.0:
		return cs.Degraded
	default:
		return cs.Critical
	}
}

// GetColorForCPU returns color based on CPU usage percentage
func (cs *ColorScheme) GetColorForCPU(cpuPercent float64) tcell.Color {
	switch {
	case cpuPercent < 50:
		return cs.Good
	case cpuPercent < 70:
		return cs.Normal
	case cpuPercent < 85:
		return cs.Degraded
	default:
		return cs.Critical
	}
}

// GetColorForMemory returns color based on memory usage percentage
func (cs *ColorScheme) GetColorForMemory(memPercent float64) tcell.Color {
	switch {
	case memPercent < 60:
		return cs.Good
	case memPercent < 75:
		return cs.Normal
	case memPercent < 90:
		return cs.Degraded
	default:
		return cs.Critical
	}
}

// GetColorForHealth returns color based on health score (0-100)
func (cs *ColorScheme) GetColorForHealth(score float64) tcell.Color {
	switch {
	case score >= 95:
		return cs.Healthy
	case score >= 80:
		return cs.Normal
	case score >= 60:
		return cs.Degraded
	default:
		return cs.Critical
	}
}

// GetSeverityColor returns color based on severity level
func (cs *ColorScheme) GetSeverityColor(severity string) tcell.Color {
	switch severity {
	case "info":
		return cs.Normal
	case "warning":
		return cs.Warning
	case "critical":
		return cs.Critical
	default:
		return cs.Muted
	}
}

// StyleBuilder helps build styled text with colors
type StyleBuilder struct {
	scheme *ColorScheme
}

// NewStyleBuilder creates a new style builder
func NewStyleBuilder(scheme *ColorScheme) *StyleBuilder {
	return &StyleBuilder{scheme: scheme}
}

// BuildStyle creates a tcell style with the specified color
func (sb *StyleBuilder) BuildStyle(color tcell.Color) tcell.Style {
	return tcell.StyleDefault.Foreground(color).Background(tcell.ColorBlack)
}

// BuildBoldStyle creates a bold tcell style
func (sb *StyleBuilder) BuildBoldStyle(color tcell.Color) tcell.Style {
	return tcell.StyleDefault.Foreground(color).Background(tcell.ColorBlack).Bold(true)
}

// BuildHighlightStyle creates a highlighted style (inverted colors)
func (sb *StyleBuilder) BuildHighlightStyle(color tcell.Color) tcell.Style {
	return tcell.StyleDefault.Background(color).Foreground(tcell.ColorBlack).Bold(true)
}