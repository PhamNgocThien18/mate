package console

import (
	"strings"
	"testing"

	tea "github.com/charmbracelet/bubbletea"

	"github.com/nguyenngocanh94/mate/internal/coreapi"
	"github.com/nguyenngocanh94/mate/internal/query"
)

// bxNews puts open news on payments-api's Mate: #5 owed, newest, and #4
// the Mate works on.
func bxNews(t *testing.T) (Model, *bxAction, *stageSpy) {
	t.Helper()
	m, act, spy := bxPayments(t, 40, 36)
	m.tree.Projects[7].Mate.News = query.KnownField([]query.NewsItem{
		{ID: 5, Crew: "k3", Kind: "budget", Title: "Ledger crew over its token budget", Question: "Let it run on today?", Owed: true, At: bxAt(14, 0)},
		{ID: 4, Title: "PR #12 merged", At: bxAt(13, 0)},
	})
	return m, act, spy
}

// A news the captain owes waits in the box beside the crews, newest first;
// one the Mate works on does not.
func TestTheBoxHoldsTheNewsOwed(t *testing.T) {
	m, _, _ := bxNews(t)
	items := m.boxItems()
	if len(items) != 2 || items[0].news == nil || items[0].news.ID != 5 || items[1].news != nil {
		t.Fatalf("box = %+v, want news #5 then k3's question", items)
	}
	m = bxFocusBox(t, m)
	frame := renderFrame(t, m)
	for _, want := range []string{"news #5", "Ledger crew over its", "k3 · budget", "Let it run on today?", "Enter answers it in the Mate", "enter news"} {
		if !strings.Contains(frame, want) {
			t.Errorf("the frame lacks %q:\n%s", want, frame)
		}
	}
}

// Enter on a news shows the Mate and asks its REPL to open the news; a
// news is not assigned, it is answered.
func TestBoxEnterOpensTheNewsInTheMate(t *testing.T) {
	m, act, spy := bxNews(t)
	m = bxFocusBox(t, m)
	m, cmd := send(t, m, key("a"))
	if cmd != nil || len(act.reqs) != 0 || !strings.Contains(m.msg.text, "Enter answers it in the Mate") {
		t.Fatalf("a on a news: reqs %+v, message %+v", act.reqs, m.msg)
	}
	_, cmd = send(t, m, key("enter"))
	if cmd == nil {
		t.Fatal("Enter on a news did nothing")
	}
	batch, ok := cmd().(tea.BatchMsg)
	if !ok {
		t.Fatal("Enter on a news did not both stage the Mate and open the news")
	}
	for _, c := range batch {
		if c != nil {
			c()
		}
	}
	if len(spy.calls) != 1 || spy.calls[0].Kind != StageMate {
		t.Fatalf("stage calls = %+v, want the Mate", spy.calls)
	}
	if len(act.reqs) != 1 || act.reqs[0].Action != coreapi.ActionOpenNews || act.reqs[0].Target != "payments-api" || act.reqs[0].Input != "5" {
		t.Fatalf("requests = %+v, want open_news #5 of payments-api", act.reqs)
	}
}

// The Mate's detail lists its open news, and whose move each one is.
func TestTheMateDetailListsItsNews(t *testing.T) {
	m, _, _ := bxNews(t)
	var text []string
	for _, f := range m.mateNewsFields(m.tree.Projects[7].Mate, 30) {
		for _, l := range f.lines {
			text = append(text, l.render(60, plainPalette()))
		}
	}
	got := strings.Join(text, "\n")
	for _, want := range []string{"#5 waiting on you", "Ledger crew over its token", "#4 the Mate works on it", "PR #12 merged"} {
		if !strings.Contains(got, want) {
			t.Errorf("news fields lack %q:\n%s", want, got)
		}
	}
}
