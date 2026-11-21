package actions

import (
	"context"
	"errors"
	"io"
	"os"
	"slices"
	"strings"
)

// Method es cómo se limpia un elemento.
type Method string

const (
	MethodTrash   Method = "trash"
	MethodCommand Method = "command"
)

// Status es cómo terminó un elemento.
type Status string

const (
	StatusDone     Status = "done"
	StatusRefused  Status = "refused"
	StatusFailed   Status = "failed"
	StatusCanceled Status = "canceled"
)

// Request es un elemento que el usuario confirmó limpiar.
type Request struct {
	Path            string
	Method          Method
	Command         []string
	ConfirmedReview bool
}

// Outcome es el resultado de un elemento. Trashed son los bytes que quedaron en la
// papelera: siguen ocupando disco hasta que se vacíe.
type Outcome struct {
	Path    string `json:"path"`
	Method  Method `json:"method"`
	Status  Status `json:"status"`
	Code    Code   `json:"code"`
	Trashed int64  `json:"trashed"`
}

// Cleaner ejecuta una lista de elementos, uno por uno.
type Cleaner struct {
	Guard Guard
	// Trash y Command se reemplazan en tests.
	Trash   func(path string, canceled func() bool) (string, error)
	Command func(ctx context.Context, argv []string, output io.Writer) error
	Output  io.Writer
	// Room dice cuánto cabe en la papelera. Nil lo mide en el disco real.
	Room func(path string) (int64, error)
}

// binTally lleva cuánto cabe en la papelera durante una limpieza. Se mide una sola
// vez: medir es lento con la papelera llena, y lo enviado se resta.
type binTally struct {
	known bool
	left  int64
}

// reserve rechaza lo que no cabe. Sin esto, Windows descartaría de forma permanente
// lo más viejo de la papelera, incluido lo que el usuario ya tenía ahí.
func (c Cleaner) reserve(tally *binTally, path string, size int64) error {
	if !tally.known {
		room := c.Room
		if room == nil {
			room = func(path string) (int64, error) {
				space, err := RecycleBinSpace(path)
				return space.Room, err
			}
		}
		left, err := room(path)
		if err != nil {
			return err
		}
		tally.known, tally.left = true, left
	}
	if size > tally.left {
		return refuse(CodeBinFull)
	}
	return nil
}

// Run procesa requests en orden. Un fallo no detiene a los demás; cancelar sí. Cada
// ruta se valida justo antes de tocarla.
func (c Cleaner) Run(ctx context.Context, requests []Request, onStart func(path string), onDone func(Outcome)) {
	var tally binTally
	for _, request := range requests {
		onStart(request.Path)
		onDone(c.one(ctx, request, &tally))
	}
}

func (c Cleaner) one(ctx context.Context, request Request, tally *binTally) Outcome {
	outcome := Outcome{Path: request.Path, Method: request.Method}
	if err := c.clean(ctx, request, &outcome, tally); err != nil {
		outcome.Code = codeFor(err)
		outcome.Status = statusFor(outcome.Code)
		return outcome
	}
	outcome.Status = StatusDone
	return outcome
}

func (c Cleaner) clean(ctx context.Context, request Request, outcome *Outcome, tally *binTally) error {
	if ctx.Err() != nil {
		return refuse(CodeCanceled)
	}
	if _, err := c.Guard.Check(request.Path, request.ConfirmedReview); err != nil {
		return err
	}
	switch request.Method {
	case MethodCommand:
		return c.Command(ctx, request.Command, c.Output)
	case MethodTrash:
	default:
		return refuse(CodeFailed)
	}

	size, err := measure(ctx, request.Path)
	if err != nil {
		return refuse(CodeCanceled)
	}
	if err := checkRecycleBin(request.Path, size); err != nil {
		return err
	}
	if err := c.reserve(tally, request.Path, size); err != nil {
		return err
	}
	if _, err := c.Trash(request.Path, func() bool { return ctx.Err() != nil }); err != nil {
		return err
	}
	tally.left -= size
	outcome.Trashed = size
	return nil
}

func statusFor(code Code) Status {
	switch code {
	case CodeCanceled:
		return StatusCanceled
	case CodeNotScanned, CodeInvalidPath, CodeProtected, CodeMissing, CodeLink, CodeChanged, CodeKeep,
		CodeUnclassified, CodeManual, CodeNeedsConfirm, CodeTooBig, CodeBinOff, CodeBinFull, CodeNotDuplicate, CodeNoCopyLeft:
		return StatusRefused
	}
	return StatusFailed
}

func codeFor(err error) Code {
	if code := CodeOf(err); code != "" {
		return code
	}
	switch {
	case errors.Is(err, ErrCanceled), errors.Is(err, context.Canceled):
		return CodeCanceled
	case errors.Is(err, ErrInUse):
		return CodeInUse
	case errors.Is(err, ErrWouldDeletePermanently):
		return CodeBinRejected
	case errors.Is(err, os.ErrPermission):
		return CodeNoPermission
	}
	return CodeFailed
}

// CollapseNested quita las rutas que ya están dentro de otra de la lista, para no
// contar dos veces ni intentar borrar algo que ya se fue con su carpeta.
func CollapseNested(paths []string) []string {
	sorted := slices.Clone(paths)
	slices.SortFunc(sorted, func(a, b string) int {
		return strings.Compare(strings.ToLower(a), strings.ToLower(b))
	})
	sorted = slices.CompactFunc(sorted, strings.EqualFold)

	keep := make([]string, 0, len(sorted))
	for _, path := range sorted {
		lower := strings.ToLower(path)
		if !slices.ContainsFunc(keep, func(parent string) bool {
			return strings.HasPrefix(lower, strings.ToLower(parent)+`\`)
		}) {
			keep = append(keep, path)
		}
	}
	return keep
}
