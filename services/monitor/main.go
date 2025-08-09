// TrueNow Monitor - Professional monitoring for TrueNow Analytics Platform
package main

import (
	"context"
	"fmt"
	"log"
	"os"
	"strings"
	"time"
	
	"github.com/Milad-Afdasta/TrueNow/services/monitor/internal/collector"
	"github.com/Milad-Afdasta/TrueNow/services/monitor/internal/ui"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// Monitor represents the main monitoring application
type Monitor struct {
	app       *tview.Application
	collector *collector.Collector
	scheme    *ui.ColorScheme
	
	// UI components
	grid           *tview.Grid
	serviceTable   *tview.Table
	latencyTable   *tview.Table
	alertsList     *tview.List
	resourceView   *tview.TextView
	throughputView *tview.TextView
	detailsView    *tview.TextView
	statusBar      *tview.TextView
	
	// State
	selectedService string
	paused          bool
	refreshRate     time.Duration
	ctx             context.Context
	cancel          context.CancelFunc
}

// NewMonitor creates a new monitor instance
func NewMonitor() *Monitor {
	ctx, cancel := context.WithCancel(context.Background())
	
	return &Monitor{
		app:         tview.NewApplication(),
		collector:   collector.NewCollector(),
		scheme:      ui.DefaultColorScheme(),
		refreshRate: 2 * time.Second,
		ctx:         ctx,
		cancel:      cancel,
	}
}

// Initialize sets up the monitor
func (m *Monitor) Initialize() {
	// Initialize collector
	m.collector.Initialize()
	
	// Create UI components
	m.createUI()
	
	// Set up keyboard handlers
	m.setupKeyboardHandlers()
	
	// Start data collection
	go m.collectData()
	
	// Start UI updates
	go m.updateUI()
}

// createUI creates all UI components
func (m *Monitor) createUI() {
	// Create main grid
	m.grid = tview.NewGrid().
		SetRows(4, 0, 0, 0, 1).
		SetColumns(0, 0, 0).
		SetBorders(true)
	
	m.grid.SetBackgroundColor(tcell.ColorBlack)
	m.grid.SetBorderColor(m.scheme.Border)
	
	// Create components
	header := m.createHeader()
	m.serviceTable = m.createServiceTable()
	m.latencyTable = m.createLatencyTable()
	m.alertsList = m.createAlertsList()
	m.resourceView = m.createResourceView()
	m.throughputView = m.createThroughputView()
	m.detailsView = m.createDetailsView()
	m.statusBar = m.createStatusBar()
	
	// Layout components in grid
	// Row 0: Header
	m.grid.AddItem(header, 0, 0, 1, 3, 0, 0, false)
	
	// Row 1: Service table (spans 3 columns)
	m.grid.AddItem(m.serviceTable, 1, 0, 1, 3, 0, 0, true)
	
	// Row 2: Latency, Throughput, Resources
	m.grid.AddItem(m.latencyTable, 2, 0, 1, 1, 0, 0, false)
	m.grid.AddItem(m.throughputView, 2, 1, 1, 1, 0, 0, false)
	m.grid.AddItem(m.resourceView, 2, 2, 1, 1, 0, 0, false)
	
	// Row 3: Alerts and Details
	m.grid.AddItem(m.alertsList, 3, 0, 1, 1, 0, 0, false)
	m.grid.AddItem(m.detailsView, 3, 1, 1, 2, 0, 0, false)
	
	// Row 4: Status bar
	m.grid.AddItem(m.statusBar, 4, 0, 1, 3, 0, 0, false)
	
	// Set root
	m.app.SetRoot(m.grid, true)
}

// createHeader creates the header
func (m *Monitor) createHeader() *tview.TextView {
	header := tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignCenter)
	
	header.SetBackgroundColor(tcell.ColorBlack)
	
	headerText := fmt.Sprintf(
		"[#%06X::b]╔═══════════════════════════════════════════════════════════════════════════════════════════════════════════════════════╗[-:-:-]\n"+
		"[#%06X::b]║                                      TrueNow Analytics Platform Monitor v2.0                                         ║[-:-:-]\n"+
		"[#%06X::b]║                                  Real-time Performance & Health Monitoring Dashboard                                  ║[-:-:-]\n"+
		"[#%06X::b]╚═══════════════════════════════════════════════════════════════════════════════════════════════════════════════════════╝[-:-:-]",
		m.scheme.Border.Hex(),
		m.scheme.Header.Hex(),
		m.scheme.Secondary.Hex(),
		m.scheme.Border.Hex(),
	)
	
	header.SetText(headerText)
	return header
}

// createServiceTable creates the service status table
func (m *Monitor) createServiceTable() *tview.Table {
	table := tview.NewTable().
		SetBorders(true).
		SetSelectable(true, false).
		SetFixed(1, 0)
	
	table.SetBackgroundColor(tcell.ColorBlack)
	table.SetTitle(" Service Status Overview ").
		SetTitleColor(m.scheme.Header).
		SetBorderColor(m.scheme.Border)
	
	// Headers
	headers := []string{
		"Service", "Status", "Health", "Instances", "CPU %", "Memory %",
		"QPS", "Events/s", "P50 (ms)", "P95 (ms)", "P99 (ms)", "Errors %",
	}
	
	for i, h := range headers {
		cell := tview.NewTableCell(h).
			SetTextColor(m.scheme.Label).
			SetAlign(tview.AlignCenter).
			SetAttributes(tcell.AttrBold).
			SetSelectable(false)
		table.SetCell(0, i, cell)
	}
	
	// Initialize empty rows for services
	services := []string{
		"control-plane", "gateway", "hot-tier", "query-api",
		"stream-ingester", "watermark-service", "rebalancer", "autoscaler",
	}
	
	for i, service := range services {
		row := i + 1
		for col := 0; col < len(headers); col++ {
			cell := tview.NewTableCell("-").
				SetTextColor(m.scheme.Muted).
				SetAlign(tview.AlignCenter)
			table.SetCell(row, col, cell)
		}
		// Service name
		table.GetCell(row, 0).
			SetText(service).
			SetTextColor(m.scheme.Primary).
			SetAlign(tview.AlignLeft)
	}
	
	// Selection handler
	table.SetSelectedFunc(func(row, column int) {
		if row > 0 && row <= len(services) {
			m.selectedService = services[row-1]
			m.updateDetailsView()
		}
	})
	
	return table
}

// createLatencyTable creates the latency percentiles table
func (m *Monitor) createLatencyTable() *tview.Table {
	table := tview.NewTable().
		SetBorders(true).
		SetFixed(1, 1)
	
	table.SetBackgroundColor(tcell.ColorBlack)
	table.SetTitle(" Latency Percentiles (ms) ").
		SetTitleColor(m.scheme.Header).
		SetBorderColor(m.scheme.Border)
	
	// Headers
	table.SetCell(0, 0, tview.NewTableCell("Service").
		SetTextColor(m.scheme.Label).
		SetAttributes(tcell.AttrBold))
	
	percentiles := []string{"P50", "P90", "P95", "P99", "P99.9", "Max"}
	for i, p := range percentiles {
		table.SetCell(0, i+1, tview.NewTableCell(p).
			SetTextColor(m.scheme.Label).
			SetAlign(tview.AlignCenter).
			SetAttributes(tcell.AttrBold))
	}
	
	// Initialize rows
	services := []string{"Gateway", "Hot-Tier", "Query", "Overall"}
	for i, service := range services {
		row := i + 1
		table.SetCell(row, 0, tview.NewTableCell(service).
			SetTextColor(m.scheme.Primary))
		
		for col := 1; col <= len(percentiles); col++ {
			table.SetCell(row, col, tview.NewTableCell("-").
				SetTextColor(m.scheme.Muted).
				SetAlign(tview.AlignCenter))
		}
	}
	
	return table
}

// createAlertsList creates the alerts list
func (m *Monitor) createAlertsList() *tview.List {
	list := tview.NewList()
	list.SetBackgroundColor(tcell.ColorBlack)
	list.SetMainTextColor(m.scheme.Primary)
	list.SetSecondaryTextColor(m.scheme.Secondary)
	list.SetTitle(" Active Alerts ").
		SetTitleColor(m.scheme.Header).
		SetBorderColor(m.scheme.Border).
		SetBorder(true)
	
	// Initialize with no alerts message
	list.AddItem("ℹ No active alerts", "System healthy", 0, nil)
	
	return list
}

// createResourceView creates the resource utilization view
func (m *Monitor) createResourceView() *tview.TextView {
	view := tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(false)
	
	view.SetBackgroundColor(tcell.ColorBlack)
	view.SetTitle(" System Resources ").
		SetTitleColor(m.scheme.Header).
		SetBorderColor(m.scheme.Border).
		SetBorder(true)
	
	// Initial text
	view.SetText(fmt.Sprintf("[#%06X]Loading resource data...[-:-:-]", m.scheme.Muted.Hex()))
	
	return view
}

// createThroughputView creates the throughput view
func (m *Monitor) createThroughputView() *tview.TextView {
	view := tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(false)
	
	view.SetBackgroundColor(tcell.ColorBlack)
	view.SetTitle(" Throughput Metrics ").
		SetTitleColor(m.scheme.Header).
		SetBorderColor(m.scheme.Border).
		SetBorder(true)
	
	// Initial text
	view.SetText(fmt.Sprintf("[#%06X]Loading throughput data...[-:-:-]", m.scheme.Muted.Hex()))
	
	return view
}

// createDetailsView creates the details view
func (m *Monitor) createDetailsView() *tview.TextView {
	view := tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true)
	
	view.SetBackgroundColor(tcell.ColorBlack)
	view.SetTitle(" Service Details ").
		SetTitleColor(m.scheme.Header).
		SetBorderColor(m.scheme.Border).
		SetBorder(true)
	
	// Initial text
	view.SetText(fmt.Sprintf(
		"[#%06X]Select a service from the table above to view detailed metrics.\n\n"+
		"Use arrow keys to navigate, Enter to select, 'r' to refresh, 'p' to pause, 'q' to quit.[-:-:-]",
		m.scheme.Secondary.Hex(),
	))
	
	return view
}

// createStatusBar creates the status bar
func (m *Monitor) createStatusBar() *tview.TextView {
	m.statusBar = tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(false)
	
	m.statusBar.SetBackgroundColor(tcell.ColorBlack)
	
	m.updateStatusBar("Initializing...")
	
	return m.statusBar
}

// collectData continuously collects metrics
func (m *Monitor) collectData() {
	// Initial collection
	m.collector.CollectAll(m.ctx)
	
	ticker := time.NewTicker(m.refreshRate)
	defer ticker.Stop()
	
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			if !m.paused {
				m.collector.CollectAll(m.ctx)
			}
		}
	}
}

// updateUI continuously updates the UI
func (m *Monitor) updateUI() {
	ticker := time.NewTicker(500 * time.Millisecond)
	defer ticker.Stop()
	
	for {
		select {
		case <-m.ctx.Done():
			return
		case <-ticker.C:
			m.app.QueueUpdateDraw(func() {
				m.refreshUI()
			})
		}
	}
}

// refreshUI updates all UI components with latest data
func (m *Monitor) refreshUI() {
	services := m.collector.GetAllServiceMetrics()
	system := m.collector.GetSystemMetrics()
	alerts := m.collector.GetAlerts()
	
	// Update service table
	m.updateServiceTable(services)
	
	// Update latency table
	m.updateLatencyTable(services)
	
	// Update alerts
	m.updateAlerts(alerts)
	
	// Update resource view
	m.updateResourceView(system)
	
	// Update throughput view
	m.updateThroughputView(system, services)
	
	// Update status bar
	status := "Active"
	if m.paused {
		status = "Paused"
	}
	m.updateStatusBar(fmt.Sprintf("%s | Services: %d | Alerts: %d", 
		status, len(services), len(alerts)))
}

// updateServiceTable updates the service status table
func (m *Monitor) updateServiceTable(services map[string]*collector.ServiceMetrics) {
	serviceOrder := []string{
		"control-plane", "gateway", "hot-tier", "query-api",
		"stream-ingester", "watermark-service", "rebalancer", "autoscaler",
	}
	
	for i, name := range serviceOrder {
		row := i + 1
		
		if svc, ok := services[name]; ok {
			// Status
			status := "healthy"
			statusColor := m.scheme.Healthy
			if svc.UnhealthyInstances > 0 {
				status = "unhealthy"
				statusColor = m.scheme.Critical
			}
			m.serviceTable.GetCell(row, 1).
				SetText(status).
				SetTextColor(statusColor)
			
			// Health score
			health := float64(svc.HealthyInstances) / float64(svc.Instances) * 100
			healthColor := m.scheme.GetColorForHealth(health)
			m.serviceTable.GetCell(row, 2).
				SetText(fmt.Sprintf("%.0f%%", health)).
				SetTextColor(healthColor)
			
			// Instances
			m.serviceTable.GetCell(row, 3).
				SetText(fmt.Sprintf("%d", svc.Instances)).
				SetTextColor(m.scheme.Value)
			
			// CPU
			m.serviceTable.GetCell(row, 4).
				SetText(fmt.Sprintf("%.1f", svc.CPUUsagePercent)).
				SetTextColor(m.scheme.GetColorForCPU(svc.CPUUsagePercent))
			
			// Memory
			m.serviceTable.GetCell(row, 5).
				SetText(fmt.Sprintf("%.1f", svc.MemoryUsagePercent)).
				SetTextColor(m.scheme.GetColorForMemory(svc.MemoryUsagePercent))
			
			// QPS
			m.serviceTable.GetCell(row, 6).
				SetText(formatNumber(svc.RequestsPerSecond)).
				SetTextColor(m.scheme.Value)
			
			// Events/s
			m.serviceTable.GetCell(row, 7).
				SetText(formatNumber(svc.EventsPerSecond)).
				SetTextColor(m.scheme.Value)
			
			// Latencies
			m.serviceTable.GetCell(row, 8).
				SetText(fmt.Sprintf("%.1f", svc.LatencyP50)).
				SetTextColor(m.scheme.GetColorForLatency(svc.LatencyP50))
			
			m.serviceTable.GetCell(row, 9).
				SetText(fmt.Sprintf("%.1f", svc.LatencyP95)).
				SetTextColor(m.scheme.GetColorForLatency(svc.LatencyP95))
			
			m.serviceTable.GetCell(row, 10).
				SetText(fmt.Sprintf("%.1f", svc.LatencyP99)).
				SetTextColor(m.scheme.GetColorForLatency(svc.LatencyP99))
			
			// Error rate
			m.serviceTable.GetCell(row, 11).
				SetText(fmt.Sprintf("%.2f", svc.ErrorPercentage)).
				SetTextColor(m.scheme.GetColorForErrorRate(svc.ErrorPercentage))
		}
	}
}

// updateLatencyTable updates the latency percentiles table
func (m *Monitor) updateLatencyTable(services map[string]*collector.ServiceMetrics) {
	// Update specific service rows
	serviceMap := map[string]int{
		"gateway": 1,
		"hot-tier": 2,
		"query-api": 3,
	}
	
	for name, row := range serviceMap {
		if svc, ok := services[name]; ok {
			m.latencyTable.GetCell(row, 1).
				SetText(fmt.Sprintf("%.1f", svc.LatencyP50)).
				SetTextColor(m.scheme.P50Color)
			
			m.latencyTable.GetCell(row, 2).
				SetText(fmt.Sprintf("%.1f", svc.LatencyP90)).
				SetTextColor(m.scheme.P90Color)
			
			m.latencyTable.GetCell(row, 3).
				SetText(fmt.Sprintf("%.1f", svc.LatencyP95)).
				SetTextColor(m.scheme.P95Color)
			
			m.latencyTable.GetCell(row, 4).
				SetText(fmt.Sprintf("%.1f", svc.LatencyP99)).
				SetTextColor(m.scheme.P99Color)
			
			m.latencyTable.GetCell(row, 5).
				SetText(fmt.Sprintf("%.1f", svc.LatencyP999)).
				SetTextColor(m.scheme.P999Color)
			
			m.latencyTable.GetCell(row, 6).
				SetText(fmt.Sprintf("%.1f", svc.LatencyMax)).
				SetTextColor(m.scheme.Critical)
		}
	}
}

// updateAlerts updates the alerts list
func (m *Monitor) updateAlerts(alerts []collector.Alert) {
	m.alertsList.Clear()
	
	if len(alerts) == 0 {
		m.alertsList.AddItem("✅ No active alerts", "System healthy", 0, nil)
	} else {
		for _, alert := range alerts {
			icon := "ℹ"
			if alert.Severity == "warning" {
				icon = "⚠"
			} else if alert.Severity == "critical" {
				icon = "⛔"
			}
			
			m.alertsList.AddItem(
				fmt.Sprintf("%s %s", icon, alert.Message),
				fmt.Sprintf("%s - %s", alert.Service, alert.Timestamp.Format("15:04:05")),
				0, nil,
			)
		}
	}
}

// updateResourceView updates the resource utilization view
func (m *Monitor) updateResourceView(system *collector.SystemMetrics) {
	if system == nil {
		return
	}
	
	text := fmt.Sprintf(
		"[#%06X::b]Platform Totals:[-:-:-]\n\n"+
		"[#%06X]Health Score:[-:-:-]  [#%06X]%.1f%%[-:-:-]\n"+
		"[#%06X]Services:[-:-:-]      [#%06X]%d healthy[-:-:-]\n"+
		"[#%06X]CPU Cores:[-:-:-]     [#%06X]%d available[-:-:-]\n"+
		"[#%06X]Memory:[-:-:-]        [#%06X]%.0f GB total[-:-:-]\n"+
		"[#%06X]Disk:[-:-:-]          [#%06X]%.0f GB total[-:-:-]\n\n"+
		"[#%06X::b]Efficiency:[-:-:-]\n"+
		"[#%06X]Score:[-:-:-]         [#%06X]%.1f%%[-:-:-]\n"+
		"[#%06X]Cost/Million:[-:-:-]  [#%06X]$%.2f[-:-:-]",
		m.scheme.Label.Hex(),
		m.scheme.Secondary.Hex(), m.scheme.GetColorForHealth(system.HealthScore).Hex(), system.HealthScore,
		m.scheme.Secondary.Hex(), m.scheme.Value.Hex(), len(system.ServiceAvailability),
		m.scheme.Secondary.Hex(), m.scheme.Value.Hex(), system.TotalCPUCores,
		m.scheme.Secondary.Hex(), m.scheme.Value.Hex(), system.TotalMemoryGB,
		m.scheme.Secondary.Hex(), m.scheme.Value.Hex(), system.TotalDiskGB,
		m.scheme.Label.Hex(),
		m.scheme.Secondary.Hex(), m.scheme.Value.Hex(), system.EfficiencyScore,
		m.scheme.Secondary.Hex(), m.scheme.Highlight.Hex(), system.CostPerMillion,
	)
	
	m.resourceView.SetText(text)
}

// updateThroughputView updates the throughput metrics view
func (m *Monitor) updateThroughputView(system *collector.SystemMetrics, services map[string]*collector.ServiceMetrics) {
	if system == nil {
		return
	}
	
	// Calculate totals
	var totalBytesIn, totalBytesOut float64
	for _, svc := range services {
		totalBytesIn += svc.BytesInPerSecond
		totalBytesOut += svc.BytesOutPerSecond
	}
	
	// Get service-specific throughput safely
	var gatewayEvents, hotTierEvents, queryAPIRequests float64
	if gw, ok := services["gateway"]; ok {
		gatewayEvents = gw.EventsPerSecond
	}
	if ht, ok := services["hot-tier"]; ok {
		hotTierEvents = ht.EventsPerSecond
	}
	if qa, ok := services["query-api"]; ok {
		queryAPIRequests = qa.RequestsPerSecond
	}
	
	text := fmt.Sprintf(
		"[#%06X::b]Platform Throughput:[-:-:-]\n\n"+
		"[#%06X]Events/sec:[-:-:-]   [#%06X]%s[-:-:-]\n"+
		"[#%06X]Queries/sec:[-:-:-]  [#%06X]%s[-:-:-]\n"+
		"[#%06X]Bytes In/sec:[-:-:-] [#%06X]%s[-:-:-]\n"+
		"[#%06X]Bytes Out/sec:[-:-:-] [#%06X]%s[-:-:-]\n\n"+
		"[#%06X::b]Service Breakdown:[-:-:-]\n"+
		"[#%06X]Gateway:[-:-:-]      [#%06X]%s/s[-:-:-]\n"+
		"[#%06X]Hot-Tier:[-:-:-]     [#%06X]%s/s[-:-:-]\n"+
		"[#%06X]Query API:[-:-:-]    [#%06X]%s/s[-:-:-]",
		m.scheme.Label.Hex(),
		m.scheme.Secondary.Hex(), m.scheme.Highlight.Hex(), formatNumber(system.TotalEventsPerSecond),
		m.scheme.Secondary.Hex(), m.scheme.Highlight.Hex(), formatNumber(system.TotalQueriesPerSecond),
		m.scheme.Secondary.Hex(), m.scheme.Value.Hex(), formatBytes(totalBytesIn),
		m.scheme.Secondary.Hex(), m.scheme.Value.Hex(), formatBytes(totalBytesOut),
		m.scheme.Label.Hex(),
		m.scheme.Secondary.Hex(), m.scheme.Value.Hex(), formatNumber(gatewayEvents),
		m.scheme.Secondary.Hex(), m.scheme.Value.Hex(), formatNumber(hotTierEvents),
		m.scheme.Secondary.Hex(), m.scheme.Value.Hex(), formatNumber(queryAPIRequests),
	)
	
	m.throughputView.SetText(text)
}

// updateDetailsView updates the details view for selected service
func (m *Monitor) updateDetailsView() {
	if m.selectedService == "" {
		return
	}
	
	svc := m.collector.GetServiceMetrics(m.selectedService)
	if svc == nil {
		return
	}
	
	text := fmt.Sprintf(
		"[#%06X::b]Service: %s[-:-:-]\n\n",
		m.scheme.Header.Hex(), m.selectedService,
	)
	
	// Basic metrics
	text += fmt.Sprintf(
		"[#%06X::b]Performance:[-:-:-]\n"+
		"[#%06X]Requests/sec:[-:-:-]  [#%06X]%.0f[-:-:-]\n"+
		"[#%06X]Mean Latency:[-:-:-]  [#%06X]%.1fms[-:-:-]\n"+
		"[#%06X]Error Rate:[-:-:-]    [#%06X]%.2f%%[-:-:-]\n\n",
		m.scheme.Label.Hex(),
		m.scheme.Secondary.Hex(), m.scheme.Value.Hex(), svc.RequestsPerSecond,
		m.scheme.Secondary.Hex(), m.scheme.Value.Hex(), svc.LatencyMean,
		m.scheme.Secondary.Hex(), m.scheme.GetColorForErrorRate(svc.ErrorPercentage).Hex(), svc.ErrorPercentage,
	)
	
	// Resources
	text += fmt.Sprintf(
		"[#%06X::b]Resources:[-:-:-]\n"+
		"[#%06X]CPU Usage:[-:-:-]     [#%06X]%.1f%%[-:-:-]\n"+
		"[#%06X]Memory Usage:[-:-:-]  [#%06X]%.1f%% (%.0f MB)[-:-:-]\n"+
		"[#%06X]Goroutines:[-:-:-]    [#%06X]%d[-:-:-]\n"+
		"[#%06X]GC Pause P99:[-:-:-]  [#%06X]%.1fms[-:-:-]\n\n",
		m.scheme.Label.Hex(),
		m.scheme.Secondary.Hex(), m.scheme.GetColorForCPU(svc.CPUUsagePercent).Hex(), svc.CPUUsagePercent,
		m.scheme.Secondary.Hex(), m.scheme.GetColorForMemory(svc.MemoryUsagePercent).Hex(), 
		svc.MemoryUsagePercent, float64(svc.MemoryUsageBytes)/(1024*1024),
		m.scheme.Secondary.Hex(), m.scheme.Value.Hex(), svc.GoroutineCount,
		m.scheme.Secondary.Hex(), m.scheme.Value.Hex(), svc.GCPauseP99,
	)
	
	// Service-specific metrics
	if len(svc.CustomMetrics) > 0 {
		text += fmt.Sprintf("[#%06X::b]Service-Specific Metrics:[-:-:-]\n", m.scheme.Label.Hex())
		
		for key, value := range svc.CustomMetrics {
			text += fmt.Sprintf(
				"[#%06X]%s:[-:-:-] [#%06X]%v[-:-:-]\n",
				m.scheme.Secondary.Hex(), formatMetricName(key),
				m.scheme.Value.Hex(), value,
			)
		}
	}
	
	m.detailsView.Clear()
	m.detailsView.SetText(text)
}

// updateStatusBar updates the status bar
func (m *Monitor) updateStatusBar(status string) {
	text := fmt.Sprintf(
		"[#%06X]%s | Updated: %s | [F1] Help | [r] Refresh | [p] Pause | [q] Quit[-:-:-]",
		m.scheme.Secondary.Hex(),
		status,
		time.Now().Format("15:04:05"),
	)
	m.statusBar.SetText(text)
}

// setupKeyboardHandlers sets up keyboard event handlers
func (m *Monitor) setupKeyboardHandlers() {
	m.app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyEscape:
			m.Stop()
			return nil
		case tcell.KeyF1:
			m.showHelp()
			return nil
		}
		
		switch event.Rune() {
		case 'q', 'Q':
			m.Stop()
			return nil
		case 'r', 'R':
			go m.collector.CollectAll(m.ctx)
			m.updateStatusBar("Refreshing...")
			return nil
		case 'p', 'P':
			m.paused = !m.paused
			if m.paused {
				m.updateStatusBar("Paused")
			} else {
				m.updateStatusBar("Resumed")
			}
			return nil
		}
		
		return event
	})
}

// showHelp displays help information
func (m *Monitor) showHelp() {
	helpText := `
TrueNow Monitor v2.0 - Keyboard Shortcuts

Navigation:
  ↑/↓        Navigate services
  Tab        Switch between panels
  Enter      Select service for details

Actions:
  r          Refresh data manually
  p          Pause/Resume auto-refresh
  q          Quit application
  F1         Show this help

Press any key to continue...`
	
	modal := tview.NewModal().
		SetText(helpText).
		AddButtons([]string{"OK"}).
		SetDoneFunc(func(buttonIndex int, buttonLabel string) {
			m.app.SetRoot(m.grid, true)
		})
	
	modal.SetBackgroundColor(tcell.ColorBlack)
	m.app.SetRoot(modal, true)
}

// Run starts the monitor
func (m *Monitor) Run() error {
	m.Initialize()
	return m.app.Run()
}

// Stop stops the monitor
func (m *Monitor) Stop() {
	m.cancel()
	m.app.Stop()
}

// Helper functions

func formatNumber(n float64) string {
	if n >= 1e9 {
		return fmt.Sprintf("%.1fB", n/1e9)
	} else if n >= 1e6 {
		return fmt.Sprintf("%.1fM", n/1e6)
	} else if n >= 1e3 {
		return fmt.Sprintf("%.1fK", n/1e3)
	}
	return fmt.Sprintf("%.0f", n)
}

func formatBytes(bytes float64) string {
	if bytes >= 1e9 {
		return fmt.Sprintf("%.1f GB", bytes/1e9)
	} else if bytes >= 1e6 {
		return fmt.Sprintf("%.1f MB", bytes/1e6)
	} else if bytes >= 1e3 {
		return fmt.Sprintf("%.1f KB", bytes/1e3)
	}
	return fmt.Sprintf("%.0f B", bytes)
}

func formatMetricName(name string) string {
	// Convert snake_case to Title Case
	parts := strings.Split(name, "_")
	for i, part := range parts {
		if len(part) > 0 {
			parts[i] = strings.ToUpper(part[:1]) + part[1:]
		}
	}
	return strings.Join(parts, " ")
}

func main() {
	// Parse command line arguments
	for _, arg := range os.Args[1:] {
		if arg == "--help" || arg == "-h" {
			fmt.Println("TrueNow Analytics Platform Monitor")
			fmt.Println("\nUsage: monitor [options]")
			fmt.Println("  --help, -h        Show this help message")
			fmt.Println("\nKeyboard shortcuts available when running (press F1 for help)")
			fmt.Println("\nMonitor always connects to real services for live data.")
			os.Exit(0)
		}
	}
	
	// Print startup banner
	fmt.Println("╔═══════════════════════════════════════════════════════════════════╗")
	fmt.Println("║           TrueNow Analytics Platform Monitor v2.0                ║")
	fmt.Println("║                                                                   ║")
	fmt.Println("║  Real-time monitoring for 100M+ events/sec analytics platform    ║")
	fmt.Println("╚═══════════════════════════════════════════════════════════════════╝")
	fmt.Println()
	
	fmt.Println("Running in LIVE mode (connecting to real services)")
	
	fmt.Println("Starting monitor...")
	time.Sleep(1 * time.Second)
	
	// Create and run monitor
	monitor := NewMonitor()
	if err := monitor.Run(); err != nil {
		log.Fatal(err)
	}
}