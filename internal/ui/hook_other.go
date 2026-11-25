//go:build !windows

package ui

type windowHook struct{}

func (a *App) hookClose() {}

func (a *App) Show() {}