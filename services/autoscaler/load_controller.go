package main

import (
    "fmt"
    "os"
    "time"
    "math"
)

func main() {
    fmt.Println("Load Controller Started - Generating realistic load patterns")
    fmt.Println("══════════════════════════════════════════════════════════")
    
    startTime := time.Now()
    
    for {
        elapsed := time.Since(startTime).Seconds()
        
        // Create a realistic load pattern with multiple waves
        // Main wave: 2-minute cycle
        mainWave := 50 + 40*math.Sin(elapsed/60.0)
        
        // Secondary wave: 30-second cycle
        secondaryWave := 10 * math.Sin(elapsed/15.0)
        
        // Spike events every 90 seconds
        spike := 0.0
        if int(elapsed)%90 < 10 {
            spike = 20.0
        }
        
        load := mainWave + secondaryWave + spike
        
        // Ensure load stays in 0-100 range
        if load < 5 {
            load = 5
        }
        if load > 95 {
            load = 95
        }
        
        os.WriteFile("/tmp/current_load.txt", []byte(fmt.Sprintf("%.0f", load)), 0644)
        
        // Log the load level with visual indicator
        bars := int(load / 5)
        barStr := ""
        for i := 0; i < 20; i++ {
            if i < bars {
                barStr += "█"
            } else {
                barStr += "░"
            }
        }
        
        fmt.Printf("\r[%s] Load: %.0f%% %s", 
            time.Now().Format("15:04:05"), 
            load, 
            barStr)
        
        time.Sleep(2 * time.Second)
    }
}
