package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// multiFlag collects repeated flags (e.g. --tag a --tag b).
type multiFlag []string

func (m *multiFlag) String() string { return strings.Join(*m, ",") }
func (m *multiFlag) Set(v string) error {
	*m = append(*m, v)
	return nil
}

func serverBase(s string) string {
	if s == "" {
		s = "127.0.0.1:8765"
	}
	if !strings.HasPrefix(s, "http") {
		s = "http://" + s
	}
	return s
}

func httpJSON(method, url string, body any) ([]byte, int, error) {
	var r io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		r = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, url, r)
	if err != nil {
		return nil, 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
	if err != nil {
		return nil, 0, fmt.Errorf("is joyvend running? %w", err)
	}
	defer resp.Body.Close()
	data, _ := io.ReadAll(resp.Body)
	return data, resp.StatusCode, nil
}

func cmdRetain(args []string) error {
	fs := flag.NewFlagSet("retain", flag.ContinueOnError)
	bank := fs.String("bank", "default", "memory bank")
	server := fs.String("server", "", "server address")
	var tags multiFlag
	fs.Var(&tags, "tag", "tag (repeatable)")
	if err := fs.Parse(args); err != nil {
		return err
	}
	content := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if content == "" {
		return errors.New("usage: joyvend retain [--bank b] [--tag t]... <content>")
	}
	item := map[string]any{"content": content}
	if len(tags) > 0 {
		item["tags"] = []string(tags)
	}
	data, code, err := httpJSON("POST", serverBase(*server)+"/v1/banks/"+*bank+"/retain",
		map[string]any{"items": []any{item}})
	if err != nil {
		return err
	}
	if code != 200 {
		return fmt.Errorf("server returned %d: %s", code, strings.TrimSpace(string(data)))
	}
	fmt.Println("remembered.")
	return nil
}

func cmdRecall(args []string) error {
	fs := flag.NewFlagSet("recall", flag.ContinueOnError)
	bank := fs.String("bank", "default", "memory bank")
	server := fs.String("server", "", "server address")
	asJSON := fs.Bool("json", false, "raw JSON output")
	if err := fs.Parse(args); err != nil {
		return err
	}
	query := strings.TrimSpace(strings.Join(fs.Args(), " "))
	if query == "" {
		return errors.New("usage: joyvend recall [--bank b] [--json] <query>")
	}
	data, code, err := httpJSON("POST", serverBase(*server)+"/v1/banks/"+*bank+"/recall",
		map[string]any{"query": query})
	if err != nil {
		return err
	}
	if code != 200 {
		return fmt.Errorf("server returned %d: %s", code, strings.TrimSpace(string(data)))
	}
	if *asJSON {
		fmt.Println(string(data))
		return nil
	}
	var out struct {
		Results []struct {
			Text string   `json:"text"`
			Tags []string `json:"tags"`
		} `json:"results"`
	}
	_ = json.Unmarshal(data, &out)
	if len(out.Results) == 0 {
		fmt.Println("(no matches)")
		return nil
	}
	for _, r := range out.Results {
		line := "• " + r.Text
		if len(r.Tags) > 0 {
			line += "  [" + strings.Join(r.Tags, ", ") + "]"
		}
		fmt.Println(line)
	}
	return nil
}

func cmdMemories(args []string) error {
	fs := flag.NewFlagSet("memories", flag.ContinueOnError)
	bank := fs.String("bank", "default", "memory bank")
	server := fs.String("server", "", "server address")
	limit := fs.Int("limit", 50, "max items")
	if err := fs.Parse(args); err != nil {
		return err
	}
	data, code, err := httpJSON("GET",
		fmt.Sprintf("%s/v1/banks/%s/memories?limit=%d", serverBase(*server), *bank, *limit), nil)
	if err != nil {
		return err
	}
	if code != 200 {
		return fmt.Errorf("server returned %d: %s", code, strings.TrimSpace(string(data)))
	}
	var out struct {
		Items []struct {
			Text string `json:"text"`
		} `json:"items"`
		Total int `json:"total"`
	}
	_ = json.Unmarshal(data, &out)
	for _, m := range out.Items {
		fmt.Println("• " + m.Text)
	}
	fmt.Printf("(%d of %d)\n", len(out.Items), out.Total)
	return nil
}

func cmdBanks(args []string) error {
	fs := flag.NewFlagSet("banks", flag.ContinueOnError)
	server := fs.String("server", "", "server address")
	if err := fs.Parse(args); err != nil {
		return err
	}
	data, code, err := httpJSON("GET", serverBase(*server)+"/v1/banks", nil)
	if err != nil {
		return err
	}
	if code != 200 {
		return fmt.Errorf("server returned %d: %s", code, strings.TrimSpace(string(data)))
	}
	var out struct {
		Banks []struct {
			BankID    string `json:"bank_id"`
			FactCount int    `json:"fact_count"`
		} `json:"banks"`
	}
	_ = json.Unmarshal(data, &out)
	if len(out.Banks) == 0 {
		fmt.Println("(no banks yet)")
		return nil
	}
	for _, b := range out.Banks {
		fmt.Printf("%-20s %d memories\n", b.BankID, b.FactCount)
	}
	return nil
}
