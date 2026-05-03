package researchadapter

import (
	"fmt"
	"strings"
)

func formatSearchResults(results []searchQueryResult) string {
	var builder strings.Builder
	builder.WriteString("# Web Search Results\n\n")
	for _, result := range results {
		builder.WriteString("## Query: ")
		builder.WriteString(result.Query)
		builder.WriteString("\n\n")
		if result.Error != "" {
			builder.WriteString("Error: ")
			builder.WriteString(result.Error)
			builder.WriteString("\n\n")
			continue
		}
		if len(result.Items) == 0 {
			builder.WriteString("No results.\n\n")
			continue
		}
		for i, item := range result.Items {
			builder.WriteString(fmt.Sprintf("### %d. %s\n\n", i+1, defaultString(item.Title, item.URL)))
			builder.WriteString("- URL: ")
			builder.WriteString(item.URL)
			builder.WriteString("\n")
			if item.Engine != "" {
				builder.WriteString("- Engine: ")
				builder.WriteString(item.Engine)
				builder.WriteString("\n")
			}
			if item.Content != "" {
				builder.WriteString("- Snippet: ")
				builder.WriteString(strings.TrimSpace(item.Content))
				builder.WriteString("\n")
			}
			builder.WriteString("\n")
		}
	}
	return strings.TrimSpace(builder.String())
}

func formatScrapeResults(results []scrapeResult) string {
	var builder strings.Builder
	builder.WriteString("# Scrape Results\n\n")
	for _, result := range results {
		builder.WriteString("## ")
		builder.WriteString(result.URL)
		builder.WriteString("\n\n")
		if result.Error != "" {
			builder.WriteString("Error: ")
			builder.WriteString(result.Error)
			builder.WriteString("\n\n")
			continue
		}
		if result.Title != "" {
			builder.WriteString("Title: ")
			builder.WriteString(result.Title)
			builder.WriteString("\n\n")
		}
		if result.Status != 0 {
			builder.WriteString(fmt.Sprintf("Status: %d\n\n", result.Status))
		}
		builder.WriteString(result.Text)
		if result.Truncated {
			builder.WriteString("\n\n(Result truncated.)")
		}
		builder.WriteString("\n\n")
	}
	return strings.TrimSpace(builder.String())
}

func formatSecurityResults(query string, results securityResults) string {
	var builder strings.Builder
	builder.WriteString("# Security Search Results\n\n")
	builder.WriteString("Query: ")
	builder.WriteString(query)
	builder.WriteString("\n\n")
	if len(results.OSV) == 0 && len(results.NVD) == 0 && len(results.CISAKEV) == 0 {
		builder.WriteString("No matching vulnerability records found.\n\n")
	}
	if len(results.OSV) > 0 {
		builder.WriteString("## OSV\n\n")
		for _, vuln := range results.OSV {
			builder.WriteString("### ")
			builder.WriteString(vuln.ID)
			builder.WriteString("\n\n")
			if len(vuln.Aliases) > 0 {
				builder.WriteString("- Aliases: ")
				builder.WriteString(strings.Join(vuln.Aliases, ", "))
				builder.WriteString("\n")
			}
			if vuln.Summary != "" {
				builder.WriteString("- Summary: ")
				builder.WriteString(vuln.Summary)
				builder.WriteString("\n")
			}
			if vuln.Modified != "" {
				builder.WriteString("- Modified: ")
				builder.WriteString(vuln.Modified)
				builder.WriteString("\n")
			}
			builder.WriteString("\n")
		}
	}
	if len(results.NVD) > 0 {
		builder.WriteString("## NVD\n\n")
		for _, vuln := range results.NVD {
			builder.WriteString("### ")
			builder.WriteString(vuln.ID)
			builder.WriteString("\n\n")
			if vuln.Severity != "" || vuln.Score != 0 {
				builder.WriteString(fmt.Sprintf("- Severity: %s %.1f\n", vuln.Severity, vuln.Score))
			}
			if vuln.Status != "" {
				builder.WriteString("- Status: ")
				builder.WriteString(vuln.Status)
				builder.WriteString("\n")
			}
			if vuln.Published != "" {
				builder.WriteString("- Published: ")
				builder.WriteString(vuln.Published)
				builder.WriteString("\n")
			}
			if vuln.Description != "" {
				builder.WriteString("- Description: ")
				builder.WriteString(vuln.Description)
				builder.WriteString("\n")
			}
			builder.WriteString("\n")
		}
	}
	if len(results.CISAKEV) > 0 {
		builder.WriteString("## CISA Known Exploited Vulnerabilities\n\n")
		for _, vuln := range results.CISAKEV {
			builder.WriteString("### ")
			builder.WriteString(vuln.CVE)
			builder.WriteString("\n\n")
			if vuln.VulnerabilityName != "" {
				builder.WriteString("- Name: ")
				builder.WriteString(vuln.VulnerabilityName)
				builder.WriteString("\n")
			}
			if vuln.VendorProject != "" || vuln.Product != "" {
				builder.WriteString("- Product: ")
				builder.WriteString(strings.TrimSpace(vuln.VendorProject + " " + vuln.Product))
				builder.WriteString("\n")
			}
			if vuln.DateAdded != "" {
				builder.WriteString("- Date added: ")
				builder.WriteString(vuln.DateAdded)
				builder.WriteString("\n")
			}
			if vuln.DueDate != "" {
				builder.WriteString("- Due date: ")
				builder.WriteString(vuln.DueDate)
				builder.WriteString("\n")
			}
			if vuln.KnownRansomware != "" {
				builder.WriteString("- Known ransomware use: ")
				builder.WriteString(vuln.KnownRansomware)
				builder.WriteString("\n")
			}
			builder.WriteString("\n")
		}
	}
	if len(results.Errors) > 0 {
		builder.WriteString("## Upstream Errors\n\n")
		for _, err := range results.Errors {
			builder.WriteString("- ")
			builder.WriteString(err)
			builder.WriteString("\n")
		}
	}
	return strings.TrimSpace(builder.String())
}

func defaultString(value, fallback string) string {
	if strings.TrimSpace(value) != "" {
		return value
	}
	return fallback
}
