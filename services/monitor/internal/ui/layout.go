package ui

import (
	"fmt"
	"time"
	"github.com/gdamore/tcell/v2"
	"github.com/rivo/tview"
)

// Layout manages the overall TUI layout
type Layout struct {
	app    *tview.Application
	pages  *tview.Pages
	scheme *ColorScheme
	
	// Main sections
	header         *tview.TextView
	statusBar      *tview.TextView
	serviceGrid    *tview.Grid
	metricsPages   *tview.Pages
	alertsPanel    *tview.List
	detailsPanel   *tview.Flex
	
	// Service components
	serviceTable   *tview.Table
	latencyTable   *tview.Table
	throughputView *tview.TextView
	resourceView   *tview.TextView
	
	// Detail views
	hotTierView    *tview.TextView
	kafkaView      *tview.TextView
	queryView      *tview.TextView
	
	// Current view state
	currentService string
	currentView    string
}

// NewLayout creates a new layout manager
func NewLayout(scheme *ColorScheme) *Layout {
	return &Layout{
		app:          tview.NewApplication(),
		pages:        tview.NewPages(),
		scheme:       scheme,
		metricsPages: tview.NewPages(),
		currentView:  "overview",
	}
}

// Initialize sets up the complete layout
func (l *Layout) Initialize() {
	// Create all components
	l.createHeader()
	l.createStatusBar()
	l.createServiceTable()
	l.createLatencyTable()
	l.createThroughputView()
	l.createResourceView()
	l.createAlertsPanel()
	l.createDetailViews()
	
	// Build main layout
	mainLayout := l.buildMainLayout()
	
	// Add pages for different views
	l.pages.AddPage("main", mainLayout, true, true)
	l.pages.AddPage("help", l.createHelpPage(), true, false)
	
	// Set up navigation
	l.setupNavigation()
	
	// Configure app
	l.app.SetRoot(l.pages, true)
	l.app.SetFocus(l.serviceTable)
}

// createHeader creates the header section
func (l *Layout) createHeader() {
	l.header = tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignCenter).
		SetScrollable(false)
	
	l.header.SetBackgroundColor(tcell.ColorBlack)
	l.header.SetTextColor(l.scheme.Header)
	
	headerText := fmt.Sprintf(
		"[%s::b]╔════════════════════════════════════════════════════════════════════════════╗[-:-:-]\n"+
		"[%s::b]║              TrueNow Analytics Platform Monitor v2.0                          ║[-:-:-]\n"+
		"[%s::b]║         Real-time Performance & Health Monitoring Dashboard                   ║[-:-:-]\n"+
		"[%s::b]╚════════════════════════════════════════════════════════════════════════════╝[-:-:-]",
		l.colorToString(l.scheme.Border),
		l.colorToString(l.scheme.Header),
		l.colorToString(l.scheme.Secondary),
		l.colorToString(l.scheme.Border),
	)
	
	l.header.SetText(headerText)
}

// createStatusBar creates the bottom status bar
func (l *Layout) createStatusBar() {
	l.statusBar = tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(false)
	
	l.statusBar.SetBackgroundColor(tcell.ColorBlack)
	l.updateStatusBar("Ready")
}

// createServiceTable creates the main service status table
func (l *Layout) createServiceTable() {
	l.serviceTable = tview.NewTable().
		SetBorders(true).
		SetSelectable(true, false).
		SetFixed(1, 0)
	
	l.serviceTable.SetBackgroundColor(tcell.ColorBlack)
	
	// Set headers
	headers := []string{
		"Service", "Status", "Instances", "CPU %", "Memory %",
		"QPS", "P50", "P90", "P95", "P99", "Errors/s", "Health",
	}
	
	for col, header := range headers {
		cell := tview.NewTableCell(header).
			SetTextColor(l.scheme.Label).
			SetAlign(tview.AlignCenter).
			SetSelectable(false).
			SetAttributes(tcell.AttrBold)
		l.serviceTable.SetCell(0, col, cell)
	}
}

// createLatencyTable creates the latency percentiles table
func (l *Layout) createLatencyTable() {
	l.latencyTable = tview.NewTable().
		SetBorders(true).
		SetFixed(1, 1)
	
	l.latencyTable.SetBackgroundColor(tcell.ColorBlack)
	
	// Headers
	l.latencyTable.SetCell(0, 0, tview.NewTableCell("Metric").
		SetTextColor(l.scheme.Label).
		SetAttributes(tcell.AttrBold))
	
	percentiles := []string{"P50", "P90", "P95", "P99", "P99.9", "Max"}
	for i, p := range percentiles {
		l.latencyTable.SetCell(0, i+1, tview.NewTableCell(p).
			SetTextColor(l.scheme.Label).
			SetAlign(tview.AlignCenter).
			SetAttributes(tcell.AttrBold))
	}
}

// createThroughputView creates the throughput metrics view
func (l *Layout) createThroughputView() {
	l.throughputView = tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true)
	
	l.throughputView.SetBackgroundColor(tcell.ColorBlack)
	l.throughputView.SetBorder(true).
		SetTitle(" Throughput Metrics ").
		SetTitleColor(l.scheme.Header).
		SetBorderColor(l.scheme.Border)
}

// createResourceView creates the resource utilization view
func (l *Layout) createResourceView() {
	l.resourceView = tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true)
	
	l.resourceView.SetBackgroundColor(tcell.ColorBlack)
	l.resourceView.SetBorder(true).
		SetTitle(" Resource Utilization ").
		SetTitleColor(l.scheme.Header).
		SetBorderColor(l.scheme.Border)
}

// createAlertsPanel creates the alerts panel
func (l *Layout) createAlertsPanel() {
	l.alertsPanel = tview.NewList().
		ShowSecondaryText(true)
	
	l.alertsPanel.SetBackgroundColor(tcell.ColorBlack)
	l.alertsPanel.SetMainTextColor(l.scheme.Primary)
	l.alertsPanel.SetSecondaryTextColor(l.scheme.Secondary)
	l.alertsPanel.SetBorder(true).
		SetTitle(" Active Alerts ").
		SetTitleColor(l.scheme.Header).
		SetBorderColor(l.scheme.Border)
}

// createDetailViews creates service-specific detail views
func (l *Layout) createDetailViews() {
	// Hot-tier detail view
	l.hotTierView = tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true)
	l.hotTierView.SetBackgroundColor(tcell.ColorBlack)
	l.hotTierView.SetBorder(true).
		SetTitle(" Hot-Tier Details ").
		SetTitleColor(l.scheme.Header).
		SetBorderColor(l.scheme.Border)
	
	// Kafka detail view
	l.kafkaView = tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true)
	l.kafkaView.SetBackgroundColor(tcell.ColorBlack)
	l.kafkaView.SetBorder(true).
		SetTitle(" Kafka/Streaming Details ").
		SetTitleColor(l.scheme.Header).
		SetBorderColor(l.scheme.Border)
	
	// Query API detail view
	l.queryView = tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true)
	l.queryView.SetBackgroundColor(tcell.ColorBlack)
	l.queryView.SetBorder(true).
		SetTitle(" Query Performance ").
		SetTitleColor(l.scheme.Header).
		SetBorderColor(l.scheme.Border)
}

// buildMainLayout assembles the main layout
func (l *Layout) buildMainLayout() *tview.Flex {
	// Top section - Services overview
	topSection := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(l.serviceTable, 0, 1, true)
	
	topSection.SetBorder(true).
		SetTitle(" Service Status Overview ").
		SetTitleColor(l.scheme.Header).
		SetBorderColor(l.scheme.Border)
	
	// Middle section - Metrics
	metricsSection := tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(l.latencyTable, 0, 1, false).
		AddItem(l.throughputView, 0, 1, false).
		AddItem(l.resourceView, 0, 1, false)
	
	// Bottom section - Alerts and details
	bottomSection := tview.NewFlex().SetDirection(tview.FlexColumn).
		AddItem(l.alertsPanel, 0, 1, false).
		AddItem(l.createDetailsPanel(), 0, 2, false)
	
	// Main layout
	mainFlex := tview.NewFlex().SetDirection(tview.FlexRow).
		AddItem(l.header, 4, 0, false).
		AddItem(topSection, 0, 2, true).
		AddItem(metricsSection, 0, 2, false).
		AddItem(bottomSection, 0, 2, false).
		AddItem(l.statusBar, 1, 0, false)
	
	mainFlex.SetBackgroundColor(tcell.ColorBlack)
	
	return mainFlex
}

// createDetailsPanel creates the tabbed details panel
func (l *Layout) createDetailsPanel() *tview.Flex {
	l.detailsPanel = tview.NewFlex().SetDirection(tview.FlexRow)
	
	// Tab buttons
	tabs := tview.NewTextView().
		SetDynamicColors(true).
		SetTextAlign(tview.AlignCenter)
	tabs.SetBackgroundColor(tcell.ColorBlack)
	tabs.SetText(fmt.Sprintf(
		"[%s::b][ F1: Hot-Tier ]  [ F2: Kafka ]  [ F3: Query ]  [ F4: System ][-:-:-]",
		l.colorToString(l.scheme.Label),
	))
	
	// Content area
	l.metricsPages.AddPage("hot-tier", l.hotTierView, true, true)
	l.metricsPages.AddPage("kafka", l.kafkaView, true, false)
	l.metricsPages.AddPage("query", l.queryView, true, false)
	
	l.detailsPanel.
		AddItem(tabs, 1, 0, false).
		AddItem(l.metricsPages, 0, 1, false)
	
	l.detailsPanel.SetBorder(true).
		SetTitle(" Service Details ").
		SetTitleColor(l.scheme.Header).
		SetBorderColor(l.scheme.Border)
	
	return l.detailsPanel
}

// createHelpPage creates the help screen
func (l *Layout) createHelpPage() *tview.Flex {
	helpText := tview.NewTextView().
		SetDynamicColors(true).
		SetScrollable(true)
	
	helpText.SetBackgroundColor(tcell.ColorBlack)
	helpText.SetTextColor(l.scheme.Primary)
	
	help := fmt.Sprintf(`[%s::b]TrueNow Monitor - Keyboard Shortcuts[-:-:-]

[%s::b]Navigation:[-:-:-]
  ↑/↓        Navigate services
  Tab        Switch between panels
  Enter      Show service details
  
[%s::b]Views:[-:-:-]
  F1         Hot-Tier details
  F2         Kafka/Streaming details
  F3         Query performance
  F4         System overview
  
[%s::b]Actions:[-:-:-]
  r          Refresh data
  p          Pause/Resume updates
  a          Show/Hide alerts
  f          Filter services
  /          Search
  
[%s::b]Display:[-:-:-]
  l          Toggle log level
  d          Toggle debug info
  h/?        Show this help
  q/Esc      Quit

[%s::b]Press any key to return...[-:-:-]`,
		l.colorToString(l.scheme.Header),
		l.colorToString(l.scheme.Label),
		l.colorToString(l.scheme.Label),
		l.colorToString(l.scheme.Label),
		l.colorToString(l.scheme.Label),
		l.colorToString(l.scheme.Secondary),
	)
	
	helpText.SetText(help)
	
	helpFlex := tview.NewFlex().
		AddItem(nil, 0, 1, false).
		AddItem(tview.NewFlex().SetDirection(tview.FlexRow).
			AddItem(nil, 0, 1, false).
			AddItem(helpText, 30, 1, true).
			AddItem(nil, 0, 1, false), 80, 1, true).
		AddItem(nil, 0, 1, false)
	
	helpFlex.SetBackgroundColor(tcell.ColorBlack)
	
	return helpFlex
}

// setupNavigation configures keyboard navigation
func (l *Layout) setupNavigation() {
	l.app.SetInputCapture(func(event *tcell.EventKey) *tcell.EventKey {
		switch event.Key() {
		case tcell.KeyF1:
			l.metricsPages.SwitchToPage("hot-tier")
			return nil
		case tcell.KeyF2:
			l.metricsPages.SwitchToPage("kafka")
			return nil
		case tcell.KeyF3:
			l.metricsPages.SwitchToPage("query")
			return nil
		case tcell.KeyEscape:
			if l.pages.HasPage("help") && l.currentView == "help" {
				l.pages.SwitchToPage("main")
				l.currentView = "main"
			} else {
				l.app.Stop()
			}
			return nil
		case tcell.KeyRune:
			switch event.Rune() {
			case 'q', 'Q':
				l.app.Stop()
				return nil
			case 'h', '?':
				l.pages.SwitchToPage("help")
				l.currentView = "help"
				return nil
			case 'r', 'R':
				l.updateStatusBar("Refreshing...")
				return nil
			}
		}
		return event
	})
}

// updateStatusBar updates the status bar text
func (l *Layout) updateStatusBar(status string) {
	l.statusBar.SetText(fmt.Sprintf(
		"[%s] %s [%s] | [%s]Press 'h' for help | 'q' to quit[-:-:-]",
		l.colorToString(l.scheme.Secondary),
		status,
		time.Now().Format("15:04:05"),
		l.colorToString(l.scheme.Muted),
	))
}

// colorToString converts tcell color to tview color string
func (l *Layout) colorToString(color tcell.Color) string {
	return fmt.Sprintf("#%06X", color.Hex())
}

// Run starts the TUI application
func (l *Layout) Run() error {
	return l.app.Run()
}