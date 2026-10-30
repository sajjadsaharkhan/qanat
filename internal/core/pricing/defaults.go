// Package pricing holds Qanat's built-in model prices, in USD per 1M tokens.
//
// The table is ported from 9router's open-sse/providers/pricing.js
// MODEL_PRICING (MIT, github.com/decolua/9router), limited to the model
// families Qanat's providers serve (Claude, OpenAI, Gemini). Exact ids only:
// 9router's glob fallbacks are left out, so a model missing here stays
// unpriced and unusable until an admin prices it (D13). The control plane
// writes these as 'default' price versions; an admin price always wins.
package pricing

// Price is a rate per 1M tokens. CacheRead and CacheWrite are 9router's
// "cached" and "cache_creation"; Qanat bills reasoning tokens as output.
type Price struct {
	In, Out, CacheRead, CacheWrite float64
}

// Defaults maps a bare upstream model id to its built-in price.
var Defaults = map[string]Price{
	// Anthropic / Claude
	"claude-opus-4-6":            {In: 5.00, Out: 25.00, CacheRead: 0.50, CacheWrite: 6.25},
	"claude-opus-4-5-20251101":   {In: 5.00, Out: 25.00, CacheRead: 0.50, CacheWrite: 6.25},
	"claude-sonnet-4-6":          {In: 3.00, Out: 15.00, CacheRead: 0.30, CacheWrite: 3.75},
	"claude-sonnet-4-5-20250929": {In: 3.00, Out: 15.00, CacheRead: 0.30, CacheWrite: 3.75},
	"claude-haiku-4-5-20251001":  {In: 1.00, Out: 5.00, CacheRead: 0.10, CacheWrite: 1.25},
	"claude-sonnet-4-20250514":   {In: 3.00, Out: 15.00, CacheRead: 1.50, CacheWrite: 3.00},
	"claude-opus-4-20250514":     {In: 15.00, Out: 25.00, CacheRead: 7.50, CacheWrite: 15.00},
	"claude-3-5-sonnet-20241022": {In: 3.00, Out: 15.00, CacheRead: 1.50, CacheWrite: 3.00},
	"claude-haiku-4.5":           {In: 0.50, Out: 2.50, CacheRead: 0.05, CacheWrite: 0.50},
	"claude-opus-4.1":            {In: 5.00, Out: 25.00, CacheRead: 0.50, CacheWrite: 5.00},
	"claude-opus-4.5":            {In: 5.00, Out: 25.00, CacheRead: 0.50, CacheWrite: 5.00},
	"claude-opus-4.6":            {In: 5.00, Out: 25.00, CacheRead: 0.50, CacheWrite: 5.00},
	"claude-sonnet-4":            {In: 3.00, Out: 15.00, CacheRead: 0.30, CacheWrite: 3.00},
	"claude-sonnet-4.5":          {In: 3.00, Out: 15.00, CacheRead: 0.30, CacheWrite: 3.00},
	"claude-sonnet-4.6":          {In: 3.00, Out: 15.00, CacheRead: 0.30, CacheWrite: 3.00},
	"claude-opus-4-5-thinking":   {In: 5.00, Out: 25.00, CacheRead: 0.50, CacheWrite: 5.00},
	"claude-opus-4-6-thinking":   {In: 5.00, Out: 25.00, CacheRead: 0.50, CacheWrite: 5.00},
	"claude-opus-5-5-thinking":   {In: 4.00, Out: 20.00, CacheRead: 0.20, CacheWrite: 5.00},
	"claude-opus-5-5":            {In: 4.00, Out: 20.00, CacheRead: 0.20, CacheWrite: 5.00},
	"claude-opus-5-5-high":       {In: 4.00, Out: 20.00, CacheRead: 0.20, CacheWrite: 5.00},
	"claude-opus-5-5-medium":     {In: 4.00, Out: 20.00, CacheRead: 0.20, CacheWrite: 5.00},
	"claude-opus-5-5-low":        {In: 4.00, Out: 20.00, CacheRead: 0.20, CacheWrite: 5.00},
	"claude-fable-5":             {In: 10.00, Out: 50.00, CacheRead: 1.00, CacheWrite: 12.50},
	"claude-sonnet-5-5":          {In: 2.00, Out: 10.00, CacheRead: 0.20, CacheWrite: 2.50},
	"claude-sonnet-5-5-thinking": {In: 2.00, Out: 10.00, CacheRead: 0.20, CacheWrite: 2.50},
	"claude-sonnet-5-5-high":     {In: 2.00, Out: 10.00, CacheRead: 0.20, CacheWrite: 2.50},
	"claude-sonnet-5-5-medium":   {In: 2.00, Out: 10.00, CacheRead: 0.20, CacheWrite: 2.50},
	"claude-sonnet-5-5-low":      {In: 2.00, Out: 10.00, CacheRead: 0.20, CacheWrite: 2.50},
	"claude-sonnet-5":            {In: 2.00, Out: 10.00, CacheRead: 0.20, CacheWrite: 2.50},

	// OpenAI / GPT
	"gpt-3.5-turbo":           {In: 0.50, Out: 1.50, CacheRead: 0.25, CacheWrite: 0.50},
	"gpt-4":                   {In: 2.50, Out: 10.00, CacheRead: 1.25, CacheWrite: 2.50},
	"gpt-4-turbo":             {In: 10.00, Out: 30.00, CacheRead: 5.00, CacheWrite: 10.00},
	"gpt-4o":                  {In: 2.50, Out: 10.00, CacheRead: 1.25, CacheWrite: 2.50},
	"gpt-4o-mini":             {In: 0.15, Out: 0.60, CacheRead: 0.075, CacheWrite: 0.15},
	"gpt-4.1":                 {In: 2.50, Out: 10.00, CacheRead: 1.25, CacheWrite: 2.50},
	"gpt-5":                   {In: 1.25, Out: 10.00, CacheRead: 0.625, CacheWrite: 1.25},
	"gpt-5-mini":              {In: 0.25, Out: 2.00, CacheRead: 0.125, CacheWrite: 0.25},
	"gpt-5-codex":             {In: 1.25, Out: 10.00, CacheRead: 0.625, CacheWrite: 1.25},
	"gpt-5.1":                 {In: 1.25, Out: 10.00, CacheRead: 0.625, CacheWrite: 1.25},
	"gpt-5.1-codex":           {In: 1.25, Out: 10.00, CacheRead: 0.625, CacheWrite: 1.25},
	"gpt-5.1-codex-mini":      {In: 1.50, Out: 6.00, CacheRead: 0.75, CacheWrite: 1.50},
	"gpt-5.1-codex-mini-high": {In: 2.00, Out: 8.00, CacheRead: 1.00, CacheWrite: 2.00},
	"gpt-5.1-codex-max":       {In: 8.00, Out: 32.00, CacheRead: 4.00, CacheWrite: 8.00},
	"gpt-5.2":                 {In: 1.75, Out: 14.00, CacheRead: 0.175, CacheWrite: 1.75},
	"gpt-5.2-codex":           {In: 1.75, Out: 14.00, CacheRead: 0.175, CacheWrite: 1.75},
	"gpt-5.3-codex":           {In: 1.75, Out: 14.00, CacheRead: 0.175, CacheWrite: 1.75},
	"gpt-5.3-codex-spark":     {In: 3.00, Out: 12.00, CacheRead: 0.30, CacheWrite: 3.00},
	"gpt-5.6":                 {In: 2.50, Out: 15.00, CacheRead: 0.25, CacheWrite: 2.50},
	"gpt-5.6-luna":            {In: 1.00, Out: 6.00, CacheRead: 0.10, CacheWrite: 1.00},
	"gpt-5.6-terra":           {In: 2.50, Out: 15.00, CacheRead: 0.25, CacheWrite: 2.50},
	"gpt-5.6-sol":             {In: 5.00, Out: 30.00, CacheRead: 0.50, CacheWrite: 5.00},
	"gpt-6-astra":             {In: 10.00, Out: 50.00, CacheRead: 1.00, CacheWrite: 12.50},
	"gpt-6.1-sol":             {In: 2.00, Out: 10.00, CacheRead: 0.10, CacheWrite: 2.50},
	"gpt-6-sol":               {In: 2.00, Out: 10.00, CacheRead: 0.20, CacheWrite: 2.50},
	"gpt-6-luna":              {In: 0.10, Out: 0.50, CacheRead: 0.01, CacheWrite: 0.125},
	"o1":                      {In: 15.00, Out: 60.00, CacheRead: 7.50, CacheWrite: 15.00},
	"o1-mini":                 {In: 3.00, Out: 12.00, CacheRead: 1.50, CacheWrite: 3.00},

	// Gemini
	"gemini-3.8-flash":           {In: 1.50, Out: 7.50, CacheRead: 0.15, CacheWrite: 1.875},
	"gemini-3.8-flash-high":      {In: 1.50, Out: 7.50, CacheRead: 0.15, CacheWrite: 1.875},
	"gemini-3.8-flash-medium":    {In: 1.50, Out: 7.50, CacheRead: 0.15, CacheWrite: 1.875},
	"gemini-3.8-flash-low":       {In: 1.50, Out: 7.50, CacheRead: 0.15, CacheWrite: 1.875},
	"gemini-3.7-flash":           {In: 1.50, Out: 7.50, CacheRead: 0.15, CacheWrite: 1.875},
	"gemini-3.7-flash-high":      {In: 1.50, Out: 7.50, CacheRead: 0.15, CacheWrite: 1.875},
	"gemini-3.7-flash-medium":    {In: 1.50, Out: 7.50, CacheRead: 0.15, CacheWrite: 1.875},
	"gemini-3.7-flash-low":       {In: 1.50, Out: 7.50, CacheRead: 0.15, CacheWrite: 1.875},
	"gemini-3.6-flash":           {In: 1.50, Out: 7.50, CacheRead: 0.15, CacheWrite: 1.875},
	"gemini-3.6-flash-high":      {In: 1.50, Out: 7.50, CacheRead: 0.15, CacheWrite: 1.875},
	"gemini-3.6-flash-medium":    {In: 1.50, Out: 7.50, CacheRead: 0.15, CacheWrite: 1.875},
	"gemini-3.6-flash-low":       {In: 1.50, Out: 7.50, CacheRead: 0.15, CacheWrite: 1.875},
	"gemini-3.5-flash-lite":      {In: 0.30, Out: 2.50, CacheRead: 0.03, CacheWrite: 0.375},
	"gemini-3.5-flash-high":      {In: 0.50, Out: 3.00, CacheRead: 0.03, CacheWrite: 0.50},
	"gemini-3-flash-preview":     {In: 0.50, Out: 3.00, CacheRead: 0.03, CacheWrite: 0.50},
	"gemini-3-pro-preview":       {In: 2.00, Out: 12.00, CacheRead: 0.25, CacheWrite: 2.00},
	"gemini-3.1-pro-low":         {In: 2.00, Out: 12.00, CacheRead: 0.25, CacheWrite: 2.00},
	"gemini-3.1-pro-high":        {In: 4.00, Out: 18.00, CacheRead: 0.50, CacheWrite: 4.00},
	"gemini-pro-agent":           {In: 4.00, Out: 18.00, CacheRead: 0.50, CacheWrite: 4.00},
	"gemini-3-flash-agent":       {In: 0.50, Out: 3.00, CacheRead: 0.03, CacheWrite: 0.50},
	"gemini-3.5-flash-low":       {In: 0.50, Out: 3.00, CacheRead: 0.03, CacheWrite: 0.50},
	"gemini-3.5-flash-extra-low": {In: 0.50, Out: 3.00, CacheRead: 0.03, CacheWrite: 0.50},
	"gemini-3-flash":             {In: 0.50, Out: 3.00, CacheRead: 0.03, CacheWrite: 0.50},
	"gemini-2.5-pro":             {In: 2.00, Out: 12.00, CacheRead: 0.25, CacheWrite: 2.00},
	"gemini-2.5-flash":           {In: 0.30, Out: 2.50, CacheRead: 0.03, CacheWrite: 0.30},
	"gemini-2.5-flash-lite":      {In: 0.15, Out: 1.25, CacheRead: 0.015, CacheWrite: 0.15},
}
