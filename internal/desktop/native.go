package desktop

import (
	"context"
	"errors"
	"strings"

	"github.com/egoist/mygo"

	"github.com/cn-maul/OpenTools/pkg/tool"
)

// NativeUI implements hostservice.UI with the MyGo desktop surfaces.
type NativeUI struct{}

// OpenFile opens the native file picker.
func (NativeUI) OpenFile(ctx context.Context, title string, extensions []string) (string, error) {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent:      mygo.CallerWindow(ctx),
		Title:       title,
		ButtonLabel: "选择",
		Filters:     filters(extensions),
	})
	if err != nil || len(paths) == 0 {
		return "", err
	}
	return paths[0], nil
}

// SaveFile opens the native save dialog.
func (NativeUI) SaveFile(ctx context.Context, title, suggested string, extensions []string) (string, error) {
	path, err := mygo.Dialog.Save(mygo.SaveDialogOptions{
		Parent:      mygo.CallerWindow(ctx),
		Title:       title,
		ButtonLabel: "保存",
		DefaultPath: suggested,
		Filters:     filters(extensions),
	})
	if err != nil {
		return "", err
	}
	return path, nil
}

// PickDirectory opens the native folder picker.
func (NativeUI) PickDirectory(ctx context.Context, title string) (string, error) {
	paths, err := mygo.Dialog.Open(mygo.OpenDialogOptions{
		Parent:      mygo.CallerWindow(ctx),
		Title:       title,
		Directory:   true,
		ButtonLabel: "选择",
	})
	if err != nil || len(paths) == 0 {
		return "", err
	}
	return paths[0], nil
}

// Notify posts a system notification.
func (NativeUI) Notify(ctx context.Context, title, body string) error {
	notification := mygo.NewNotification(mygo.NotificationOptions{Title: title, Body: body})
	return notification.ShowContext(ctx)
}

// Elevate runs a command with the Windows administrator token.
func (NativeUI) Elevate(ctx context.Context, request tool.ElevateRequest) (tool.ElevateResult, error) {
	if strings.TrimSpace(request.Program) == "" {
		return tool.ElevateResult{}, errors.New("elevation request has no program")
	}
	return runElevated(ctx, request)
}

func filters(extensions []string) []mygo.FileFilter {
	cleaned := make([]string, 0, len(extensions))
	for _, extension := range extensions {
		extension = strings.TrimPrefix(strings.TrimSpace(extension), ".")
		if extension != "" {
			cleaned = append(cleaned, extension)
		}
	}
	if len(cleaned) == 0 {
		return nil
	}
	return []mygo.FileFilter{{
		Name:       strings.ToUpper(strings.Join(cleaned, ", ")) + " 文件",
		Extensions: cleaned,
	}}
}
