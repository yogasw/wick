package view

import (
	"context"
	"io"

	"github.com/a-h/templ"
)

// ProjectSettingsForbidden is the settings page a project member who is
// not its owner lands on: the refusal in the usual agents chrome with a
// way back to the board, instead of a bare error body. Plain Go rather
// than templ — one paragraph and a link need no template.
func ProjectSettingsForbidden(layout AgentsLayoutVM, base, projectID, msg string) templ.Component {
	body := templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		href := base + "/sessions?project=" + templ.EscapeString(projectID)
		_, err := io.WriteString(w, `<div class="p-8 text-sm text-gray-700 dark:text-gray-300">`+
			`<p class="font-medium">`+templ.EscapeString(msg)+`</p>`+
			`<p class="mt-2"><a class="text-blue-600 hover:underline dark:text-blue-400" href="`+
			templ.EscapeString(href)+`">Back to the project</a></p></div>`)
		return err
	})
	return templ.ComponentFunc(func(ctx context.Context, w io.Writer) error {
		return AgentsLayout(layout).Render(templ.WithChildren(ctx, body), w)
	})
}
