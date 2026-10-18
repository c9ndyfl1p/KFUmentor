package main

import (
	"flag"
	"fmt"
	"log"
	"os"
	"path/filepath"

	"github.com/mihnpro/Ai_agent_2/internal/browser"
	"github.com/mihnpro/Ai_agent_2/internal/localui"
	"github.com/mihnpro/Ai_agent_2/internal/types"
	"gopkg.in/yaml.v3"
)

func loadConfig() (types.Config, error) {
	cfg := types.Config{
		Headless:   true,
		Debug:      false,
		UseAI:      true,
		AutoApply:  false,
		ProfileDir: filepath.Join(os.Getenv("HOME"), ".ai-job-agent", "profile"),
		ModelURL:   "http://localhost:11434",
		ModelName:  "qwen2.5:1.5b",
		MaxJobs:    types.DefaultVacancyLimit,
		ResumePath: "data/resume.json",
	}

	data, err := os.ReadFile("config.yaml")
	if err != nil {
		log.Printf("Using default config: %v", err)
		return cfg, nil
	}

	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return cfg, fmt.Errorf("parse config: %w", err)
	}

	return cfg, nil
}

func main() {
	var task string
	var headless bool
	var debug bool
	var autoApply bool
	var applicationTarget int
	var vacancyLimit int
	var profileDir string
	var modelURL string
	var modelName string
	var interactive bool
	var resumePath string
	var web bool
	var webAddr string
	var loginOnly bool
	var areaID int

	flag.StringVar(&task, "task", "", "Task for the agent")
	flag.BoolVar(&headless, "headless", true, "Run browser in headless mode")
	flag.BoolVar(&debug, "debug", false, "Enable debug mode")
	flag.BoolVar(&autoApply, "auto-apply", false, "Auto apply to suitable jobs")
	flag.IntVar(&applicationTarget, "application-target", types.DefaultApplicationTarget, "Desired successful applications (1–100)")
	flag.IntVar(&vacancyLimit, "vacancy-limit", 0, "Maximum vacancies to inspect (1–100; defaults to config)")
	flag.StringVar(&profileDir, "profile", filepath.Join(os.Getenv("HOME"), ".ai-job-agent", "profile"), "Browser profile directory")
	flag.StringVar(&modelURL, "model-url", "http://localhost:11434", "Ollama model URL")
	flag.StringVar(&modelName, "model", "qwen2.5:1.5b", "Model name for Ollama")
	flag.BoolVar(&interactive, "interactive", false, "Interactive mode")
	flag.StringVar(&resumePath, "resume", "data/resume.json", "Path to candidate profile JSON")
	flag.BoolVar(&web, "web", false, "Start the local web interface")
	flag.StringVar(&webAddr, "web-addr", "127.0.0.1:8787", "Local web interface address")
	flag.BoolVar(&loginOnly, "login-only", false, "Open hh.ru for manual login without starting a search")
	flag.IntVar(&areaID, "area", 0, "hh.ru area ID for vacancy search")
	flag.Parse()

	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	if flag.Parsed() {
		cfg.Headless = headless
		cfg.Debug = debug
		cfg.AutoApply = autoApply
		if applicationTarget < 1 || applicationTarget > types.MaxApplicationTarget {
			log.Fatalf("Количество откликов должно быть от 1 до %d", types.MaxApplicationTarget)
		}
		cfg.ApplicationTarget = applicationTarget
		if vacancyLimit == 0 {
			vacancyLimit = cfg.MaxJobs
		}
		if vacancyLimit == 0 {
			vacancyLimit = types.DefaultVacancyLimit
		}
		if vacancyLimit < 1 || vacancyLimit > types.MaxVacancyLimit {
			log.Fatalf("Лимит поиска вакансий должен быть от 1 до %d", types.MaxVacancyLimit)
		}
		cfg.MaxJobs = vacancyLimit
		if profileDir != "" {
			cfg.ProfileDir = profileDir
		}
		if modelURL != "" {
			cfg.ModelURL = modelURL
		}
		if modelName != "" {
			cfg.ModelName = modelName
		}
		if resumePath != "" {
			cfg.ResumePath = resumePath
		}
		if areaID > 0 {
			cfg.AreaID = areaID
		}
	}

	if interactive {
		runInteractiveMode(cfg)
		return
	}
	if web {
		if err := localui.Start(cfg, webAddr); err != nil {
			log.Fatalf("Local web interface failed: %v", err)
		}
		return
	}
	if loginOnly {
		if err := browser.OpenLogin(cfg); err != nil {
			log.Fatalf("hh.ru login window failed: %v", err)
		}
		return
	}

	if task == "" {
		if len(flag.Args()) > 0 {
			task = flag.Args()[0]
		} else {
			fmt.Println("Enter task for agent:")
			fmt.Scanln(&task)
			if task == "" {
				task = "Найди 3 подходящие вакансии AI-инженера на hh.ru"
			}
		}
	}

	fmt.Printf("🤖 AI Job Agent Starting...\n")
	fmt.Printf("   Task: %s\n", task)
	fmt.Printf("   Model: %s at %s\n", cfg.ModelName, cfg.ModelURL)
	fmt.Printf("   Profile: %s\n", cfg.ProfileDir)
	fmt.Printf("   Mode: %s\n", func() string {
		if cfg.Headless {
			return "headless"
		}
		return "visible"
	}())
	if cfg.AutoApply {
		fmt.Printf("   ⚠️  AUTO-APPLY ENABLED\n")
	}

	if err := browser.RunTask(task, cfg); err != nil {
		log.Fatalf("❌ Agent failed: %v", err)
	}

	fmt.Println("\n✅ Agent completed successfully!")
}

func runInteractiveMode(cfg types.Config) {
	fmt.Println("🤖 AI Job Agent - Interactive Mode")
	fmt.Println("Type 'quit' or 'exit' to end")
	fmt.Println()

	for {
		fmt.Print("> ")
		var input string
		fmt.Scanln(&input)

		if input == "quit" || input == "exit" {
			fmt.Println("Goodbye!")
			return
		}

		if input != "" {
			fmt.Printf("Executing: %s\n", input)
			if err := browser.RunTask(input, cfg); err != nil {
				fmt.Printf("Error: %v\n", err)
			}
		}
	}
}
