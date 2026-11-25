package app

import (
	"log/slog"
	"sync"
	"time"

	"github.com/zoneBen/longuv/internal/hub"
	"github.com/zoneBen/longuv/internal/state"
)

// saver debounces runtime state changes and persists them atomically.
type saver struct {
	hub   *hub.Hub
	store *state.Store
	log   *slog.Logger

	dirty chan struct{}
	stop  chan struct{}
	done  chan struct{}
	once  sync.Once

	saveMu sync.Mutex
}

func newSaver(h *hub.Hub, store *state.Store, log *slog.Logger) *saver {
	s := &saver{
		hub:   h,
		store: store,
		log:   log,
		dirty: make(chan struct{}, 1),
		stop:  make(chan struct{}),
		done:  make(chan struct{}),
	}
	go s.run()
	return s
}

// Trigger requests a debounced save. It never blocks and is safe to call while
// holding device locks.
func (s *saver) Trigger() {
	select {
	case s.dirty <- struct{}{}:
	default:
	}
}

// SaveNow persists the current state synchronously.
func (s *saver) SaveNow() error {
	s.saveMu.Lock()
	defer s.saveMu.Unlock()
	file := s.hub.SnapshotFile()
	if err := s.store.Save(file); err != nil {
		s.log.Error("保存状态失败", "path", s.store.Path(), "err", err)
		return err
	}
	return nil
}

func (s *saver) run() {
	defer close(s.done)
	for {
		select {
		case <-s.stop:
			return
		case <-s.dirty:
			select {
			case <-s.stop:
				return
			case <-time.After(500 * time.Millisecond):
			}
			_ = s.SaveNow()
		}
	}
}

// Stop writes a final snapshot and stops the background saver.
func (s *saver) Stop() error {
	var err error
	s.once.Do(func() {
		err = s.SaveNow()
		close(s.stop)
		<-s.done
	})
	return err
}
