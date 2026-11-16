// Package notify zawiera konektory powiadomień (e-mail; w przyszłości Slack, Teams itd.).
// Każdy konektor to typ zarejestrowany w rejestrze: walidacja konfiguracji + fabryka.
package notify

import (
	"context"
	"sort"
)

// Message to treść powiadomienia (tekst, bez formatowania specyficznego dla kanału).
type Message struct {
	Subject string
	Body    string
}

// Connector wysyła powiadomienia jednym kanałem.
type Connector interface {
	Send(ctx context.Context, m Message) error
}

// Type opisuje rodzaj konektora.
type Type struct {
	Name      string // identyfikator w API i bazie, np. "email"
	Label     string // nazwa dla użytkownika
	HasSecret bool   // czy konektor ma sekret (hasło/token) przechowywany zaszyfrowany osobno od konfiguracji

	// Validate sprawdza konfigurację (JSON bez sekretu) i zwraca ją w postaci znormalizowanej.
	Validate func(cfg []byte) ([]byte, error)
	// New buduje konektor z poprawnej konfiguracji i sekretu.
	New func(cfg []byte, secret string) (Connector, error)
}

var registry = map[string]Type{}

func register(t Type) { registry[t.Name] = t }

// Lookup zwraca zarejestrowany typ konektora.
func Lookup(name string) (Type, bool) {
	t, ok := registry[name]
	return t, ok
}

// Types zwraca wszystkie typy posortowane po nazwie.
func Types() []Type {
	out := make([]Type, 0, len(registry))
	for _, t := range registry {
		out = append(out, t)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}
