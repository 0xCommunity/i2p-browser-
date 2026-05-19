package main

import (
	"embed"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"
)

// I2P proxy configuration
const (
	I2P_HTTP_PROXY   = "http://127.0.0.1:4444"
	I2P_SOCKS_PROXY  = "127.0.0.1:4447"
	I2P_CONTROL_PORT = "127.0.0.1:7657"
	BROWSER_PORT     = "8080"
)

//go:embed browser.html
var browserHTML embed.FS

func main() {
	fmt.Println("◈ I2P Browser v1.0.0 ◈")
	fmt.Println("Anonymous. Decentralized. Secure.\n")

	// Initialize I2P monitoring
	monitorI2P()

	// Start web server
	go startWebServer()

	// Open browser in default web browser
	browserURL := "http://127.0.0.1:" + BROWSER_PORT
	fmt.Printf("🌐 Opening browser at %s\n", browserURL)
	openBrowser(browserURL)

	fmt.Println("Press Ctrl+C to exit\n")

	// Keep running
	select {}
}

func monitorI2P() {
	fmt.Println("🔍 Monitoring I2P router status...")

	// Check if Java is available
	if !isJavaAvailable() {
		fmt.Println("❌ Java is not installed or not in PATH")
		fmt.Println("   Please install Java to run I2P router")
		fmt.Println("   Download: https://www.java.com/download/")
		fmt.Println("   After installing Java, restart this application.\n")
		return
	}
	fmt.Println("✅ Java is available")

	// Check if I2P is running
	if isI2PRunning() {
		fmt.Println("✅ I2P router is running\n")
		return
	}

	fmt.Println("ℹ️  I2P router is not running")

	// Try to start I2P
	fmt.Println("⚠️  Attempting to start I2P router...")
	if startI2P() {
		fmt.Println("⏳ Waiting for I2P router to start (this may take 1-2 minutes)...")
		if waitForI2P() {
			fmt.Println("✅ I2P router started successfully\n")
		} else {
			fmt.Println("❌ Failed to start I2P router within timeout period")
			fmt.Println("   I2P router may still be starting up. You can:")
			fmt.Println("   - Wait a few more minutes and try again")
			fmt.Println("   - Start I2P manually using your system's service manager")
			if runtime.GOOS == "windows" {
				fmt.Println("   - Run: i2prouter.bat start or use the I2P Service")
			} else {
				fmt.Println("   - Run: i2prouter start")
			}
			fmt.Println()
		}
	} else {
		fmt.Println("❌ Could not start I2P router automatically")
		fmt.Println("   Please start I2P router manually:")
		if runtime.GOOS == "windows" {
			fmt.Println("   - Look for 'I2P' in your Start Menu")
			fmt.Println("   - Or run: C:\\Program Files\\i2p\\i2prouter.bat start")
			fmt.Println("   - Or use Windows Services: Start 'I2P Router' service")
		} else {
			fmt.Println("   - Run: i2prouter start")
			fmt.Println("   - Or: systemctl start i2p (on Linux with systemd)")
			fmt.Println("   - Or: brew services start i2p (on macOS with Homebrew)")
		}
		fmt.Println("   After starting I2P, restart this application.\n")
	}
}

// expandPath expands the tilde (~) in a path to the user's home directory
func expandPath(path string) string {
	if strings.HasPrefix(path, "~/") || path == "~" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			return path // Return original if we can't get home dir
		}
		if path == "~" {
			return homeDir
		}
		return filepath.Join(homeDir, path[2:])
	}
	return path
}

// getI2PStartPaths returns platform-specific I2P startup paths
func getI2PStartPaths() []string {
	var paths []string

	if runtime.GOOS == "windows" {
		// Windows-specific paths
		paths = append(paths,
			"i2prouter.bat",
			"run.bat",
			"i2p\\i2prouter.bat",
			"i2p\\run.bat",
		)

		// Common Windows installation directories
		programFiles := os.Getenv("ProgramFiles")
		if programFiles != "" {
			paths = append(paths,
				filepath.Join(programFiles, "i2p", "i2prouter.bat"),
				filepath.Join(programFiles, "i2p", "run.bat"),
			)
		}

		programFilesX86 := os.Getenv("ProgramFiles(x86)")
		if programFilesX86 != "" {
			paths = append(paths,
				filepath.Join(programFilesX86, "i2p", "i2prouter.bat"),
				filepath.Join(programFilesX86, "i2p", "run.bat"),
			)
		}

		// Check C:\i2p
		paths = append(paths,
			"C:\\i2p\\i2prouter.bat",
			"C:\\i2p\\run.bat",
		)
	} else {
		// Unix/Linux/macOS paths
		paths = append(paths,
			"i2prouter",
			"i2p/i2prouter",
			expandPath("~/.i2p/i2prouter"),
			"/usr/bin/i2prouter",
			"/usr/local/bin/i2prouter",
			"/opt/i2p/i2prouter",
		)
	}

	return paths
}

func isJavaAvailable() bool {
	_, err := exec.LookPath("java")
	return err == nil
}

func isI2PRunning() bool {
	// Check I2P control port
	client := &http.Client{
		Timeout: 5 * time.Second, // Increased timeout to 5 seconds
		// Don't follow redirects, just check if the server responds
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	resp, err := client.Get("http://" + I2P_CONTROL_PORT + "/")
	if err != nil {
		// Log the error for debugging
		fmt.Printf("   Debug: I2P check failed - %v\n", err)
		return false
	}
	defer resp.Body.Close()
	// Accept 200 (OK), 301/302/307 (redirects) as "running"
	// I2P router console often returns 307 redirect to /home
	return resp.StatusCode == 200 ||
		resp.StatusCode == 301 ||
		resp.StatusCode == 302 ||
		resp.StatusCode == 307
}

func startI2P() bool {
	// Get platform-specific I2P startup paths
	i2pPaths := getI2PStartPaths()

	for _, path := range i2pPaths {
		cmd := exec.Command(path, "start")
		err := cmd.Start()
		if err == nil {
			return true
		}
	}

	// Try Java-based startup as fallback
	javaCmd := exec.Command("java", "-jar", "i2p.jar")
	err := javaCmd.Start()
	if err == nil {
		return true
	}

	return false
}

func waitForI2P() bool {
	maxAttempts := 30 // Increased from 5 to allow more time for I2P startup
	initialDelay := 2 * time.Second
	maxDelay := 5 * time.Second

	for i := 0; i < maxAttempts; i++ {
		// Use increasing delay: 2s for first 10 attempts, then 5s
		delay := initialDelay
		if i >= 10 {
			delay = maxDelay
		}

		fmt.Printf("   (%d/%d) Checking I2P status (waiting %v)...\n", i+1, maxAttempts, delay)
		if isI2PRunning() {
			return true
		}
		time.Sleep(delay)
	}
	return false
}

func processURL(input string) string {
	if strings.HasPrefix(input, "http://") || strings.HasPrefix(input, "https://") {
		return input
	}

	// Check if it's an I2P address
	if strings.Contains(input, ".i2p") {
		return "http://" + input
	}

	// Check if it looks like a domain
	if strings.Contains(input, ".") {
		return "http://" + input
	}

	// Default to search
	return "https://duckduckgo.com/?q=" + url.QueryEscape(input)
}

func fetchPage(urlStr string) {
	fmt.Println("⏳ Connecting to I2P network... (this may take a while)")
	client := createHTTPClient(urlStr)

	resp, err := client.Get(urlStr)
	if err != nil {
		fmt.Printf("❌ Error fetching page: %v\n", err)

		// Provide helpful suggestions for I2P sites
		if strings.Contains(urlStr, ".i2p") {
			fmt.Println("\n💡 Tips for I2P sites:")
			fmt.Println("   - I2P sites may take 1-3 minutes to load")
			fmt.Println("   - The site might be offline or not exist")
			fmt.Println("   - Check if the address is correct")
			fmt.Println("   - Try visiting http://stats.i2p/ to see available sites")
			fmt.Println("   - Your I2P router may need more peers")
		}
		fmt.Println()
		return
	}
	defer resp.Body.Close()

	fmt.Printf("\nStatus: %s\n", resp.Status)
	fmt.Println("Headers:")
	for key, values := range resp.Header {
		fmt.Printf("  %s: %s\n", key, strings.Join(values, ", "))
	}

	// Read and display content (first 2000 chars)
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		fmt.Printf("❌ Error reading response: %v\n\n", err)
		return
	}

	fmt.Println("\nContent (truncated):")
	content := string(body)
	if len(content) > 2000 {
		content = content[:2000] + "..."
	}
	fmt.Println(content)
	fmt.Println("\n" + strings.Repeat("-", 80) + "\n")
}

func createHTTPClient(urlStr string) *http.Client {
	// Check if URL is an I2P address
	if strings.Contains(urlStr, ".i2p") || strings.Contains(urlStr, "127.0.0.1:7657") {
		// Use I2P proxy for .i2p addresses
		proxyURL, _ := url.Parse(I2P_HTTP_PROXY)
		return &http.Client{
			Transport: &http.Transport{
				Proxy: http.ProxyURL(proxyURL),
				// I2P needs longer timeouts due to network latency
				DialContext: (&net.Dialer{
					Timeout:   60 * time.Second,
					KeepAlive: 30 * time.Second,
				}).DialContext,
				TLSHandshakeTimeout:   60 * time.Second,
				ResponseHeaderTimeout: 180 * time.Second, // 3 minutes for I2P
				ExpectContinueTimeout: 30 * time.Second,
			},
			Timeout: 300 * time.Second, // 5 minutes total timeout for I2P sites
		}
	}

	// Default client for regular URLs
	return &http.Client{Timeout: 30 * time.Second}
}

// openBrowser opens the default web browser to a URL
func openBrowser(url string) {
	var cmd *exec.Cmd

	switch runtime.GOOS {
	case "windows":
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	case "darwin":
		cmd = exec.Command("open", url)
	default: // Linux
		cmd = exec.Command("xdg-open", url)
	}

	err := cmd.Start()
	if err != nil {
		fmt.Printf("⚠️  Could not open browser automatically: %v\n", err)
		fmt.Printf("   Please manually open: %s\n", url)
	}
}
