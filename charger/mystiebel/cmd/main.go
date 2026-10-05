// Manual test tool for the MyStiebel cloud API.
// Usage: go run ./charger/mystiebel/cmd/ -user EMAIL -password PASS [-installation ID] [-test-set]
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"time"

	"github.com/evcc-io/evcc/charger/mystiebel"
	"github.com/evcc-io/evcc/util"
)

func main() {
	user := flag.String("user", os.Getenv("MYSTIEBEL_USER"), "MyStiebel username (or MYSTIEBEL_USER env)")
	pass := flag.String("password", os.Getenv("MYSTIEBEL_PASSWORD"), "MyStiebel password (or MYSTIEBEL_PASSWORD env)")
	installID := flag.String("installation", "", "Installation ID (optional, auto-detected)")
	testSet := flag.Bool("test-set", false, "test setValues: briefly activate boost then deactivate")
	flag.Parse()

	if *user == "" || *pass == "" {
		fmt.Fprintln(os.Stderr, "usage: -user EMAIL -password PASS")
		os.Exit(1)
	}

	logger := util.NewLogger("mystiebel-test")
	logger.Redact(*user, *pass)
	ctx := context.Background()

	fmt.Println("=== Step 1: Authenticate ===")
	auth := mystiebel.NewAuth(logger, *user, *pass, "")
	if err := auth.Login(ctx); err != nil {
		log.Fatalf("login failed: %v", err)
	}
	fmt.Println("Login OK")

	fmt.Println("\n=== Step 2: Get Installations ===")
	installations, err := auth.Installations(ctx)
	if err != nil {
		log.Fatalf("get installations: %v", err)
	}
	if len(installations) == 0 {
		log.Fatal("no installations found")
	}
	for _, inst := range installations {
		fmt.Printf("  ID: %d  Name: %s  MAC: %s\n", inst.ID, inst.Profile.Name, inst.MACAddress)
	}

	chosen := *installID
	if chosen == "" {
		chosen = fmt.Sprintf("%d", installations[0].ID)
		fmt.Printf("Using first installation: %s\n", chosen)
	}

	fmt.Println("\n=== Step 3: WebSocket — read registers ===")
	client := mystiebel.NewClient(logger, auth, chosen)
	defer client.Close()

	registers := []struct {
		id   int64
		name string
	}{
		{mystiebel.RegisterDomeTemp, "Dome Temp (15)"},
		{mystiebel.RegisterTargetTemp, "Target Temp (2378)"},
		{mystiebel.RegisterSetpointComfort, "Setpoint Comfort (13)"},
		{mystiebel.RegisterSetpointEco, "Setpoint Eco (14)"},
		{mystiebel.RegisterBoostRequest, "Boost Request (2382)"},
		{mystiebel.RegisterHotWaterPlus, "Hot Water Plus (2487)"},
		{mystiebel.RegisterSGReadyState, "SG-Ready State (2388)"},
	}

	ctx2, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()

	for _, r := range registers {
		v, err := client.GetFloat(ctx2, r.id)
		if err != nil {
			fmt.Printf("  %-35s  ERROR: %v\n", r.name, err)
		} else {
			fmt.Printf("  %-35s  = %.2f\n", r.name, v)
		}
	}

	if *testSet {
		fmt.Println("\n=== Step 4: Test setValues (boost on → off) ===")
		ctx3, cancel3 := context.WithTimeout(ctx, 30*time.Second)
		defer cancel3()

		fmt.Println("  Setting Boost Request = 1 (on)...")
		if err := client.SetValue(ctx3, mystiebel.RegisterBoostRequest, 1); err != nil {
			log.Fatalf("setValues on: %v", err)
		}
		time.Sleep(2 * time.Second)

		v, err := client.GetFloat(ctx3, mystiebel.RegisterBoostRequest)
		if err != nil {
			fmt.Printf("  Read back ERROR: %v\n", err)
		} else {
			fmt.Printf("  Boost Request after set = %.0f\n", v)
		}

		fmt.Println("  Setting Boost Request = 0 (off)...")
		if err := client.SetValue(ctx3, mystiebel.RegisterBoostRequest, 0); err != nil {
			log.Fatalf("setValues off: %v", err)
		}
		time.Sleep(3 * time.Second)

		v, err = client.GetFloat(ctx3, mystiebel.RegisterBoostRequest)
		if err != nil {
			fmt.Printf("  Read back ERROR: %v\n", err)
		} else {
			fmt.Printf("  Boost Request after reset = %.0f\n", v)
		}
	}

	fmt.Println("\n=== Done ===")
}
