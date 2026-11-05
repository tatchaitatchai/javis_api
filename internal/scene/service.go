// Package scene stores each home's scenes. Clients run a scene through their
// own device provider and report the run here.
package scene

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"strings"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"javis_api/internal/home"
	"javis_api/internal/scene/scenedb"
	"javis_api/pkg/postgres"
)

const (
	maxScenesPerHome  = 50
	maxActionsPerRun  = 50
	maxTargetKey      = 128
	minBrightness     = .01
	maxBrightness     = 1.0
	minTemperature    = 1000
	maxTemperature    = 10000
	actionLight       = "light"
	actionLockCheck   = "lock_check"
	targetDevice      = "device"
	targetZone        = "zone"
	targetOthers      = "others"
	targetAll         = "all"
	presetSleep       = "Sleep"
	presetWelcomeHome = "Welcome home"
	presetLeaveHome   = "Leave home"
	presetWakeUp      = "Wake up"
)

var (
	ErrInvalidScene = errors.New("invalid scene")
	ErrSceneLimit   = errors.New("scene limit reached")
	ErrSceneMissing = errors.New("scene not found")
)

var (
	icons      = []string{"moon", "home", "leave", "sunrise", "sparkle"}
	presetKeys = []string{presetSleep, presetWelcomeHome, presetLeaveHome, presetWakeUp}
)

// Access is the part of the home module this one needs.
type Access interface {
	RequireRole(ctx context.Context, userID, homeID uuid.UUID, minimum home.Role) error
}

type Target struct {
	Kind string  `json:"kind"`
	Key  *string `json:"key,omitempty"`
}

// Action is stored as JSON in this shape. A lock_check only checks: no action
// can unlock a door.
type Action struct {
	Type        string   `json:"type"`
	Target      *Target  `json:"target,omitempty"`
	IsOn        *bool    `json:"is_on,omitempty"`
	Brightness  *float64 `json:"brightness,omitempty"`
	Temperature *int     `json:"temperature,omitempty"`
}

type Input struct {
	Icon      string
	PresetKey *string
	Name      *string
	Actions   []Action
}

type Scene struct {
	ID        uuid.UUID
	Icon      string
	PresetKey *string
	Name      *string
	Actions   []Action
	LastRunAt *time.Time
}

type Service struct {
	pool    *pgxpool.Pool
	queries *scenedb.Queries
	access  Access
}

func NewService(pool *pgxpool.Pool) *Service {
	return &Service{pool: pool, queries: scenedb.New(pool)}
}

// UseAccess sets the home access check. It is separate from NewService because
// the home service is built with this one as its seeder.
func (s *Service) UseAccess(access Access) {
	s.access = access
}

func (s *Service) List(ctx context.Context, userID, homeID uuid.UUID) ([]Scene, error) {
	if err := s.access.RequireRole(ctx, userID, homeID, home.RoleMember); err != nil {
		return nil, err
	}
	rows, err := s.queries.ListScenes(ctx, homeID)
	if err != nil {
		return nil, err
	}
	scenes := make([]Scene, 0, len(rows))
	for _, row := range rows {
		scene, err := fromRow(row.ID, row.Icon, row.PresetKey, row.Name, row.Actions, row.LastRunAt)
		if err != nil {
			return nil, err
		}
		scenes = append(scenes, scene)
	}
	return scenes, nil
}

func (s *Service) Create(ctx context.Context, userID, homeID uuid.UUID, input Input) (Scene, error) {
	if err := s.access.RequireRole(ctx, userID, homeID, home.RoleAdmin); err != nil {
		return Scene{}, err
	}
	clean, actions, err := prepare(input)
	if err != nil {
		return Scene{}, err
	}
	var scene Scene
	err = postgres.WithTx(ctx, s.pool, func(tx pgx.Tx) error {
		queries := scenedb.New(tx)
		if err := queries.LockHome(ctx, homeID); err != nil {
			return err
		}
		count, err := queries.CountScenes(ctx, homeID)
		if err != nil {
			return err
		}
		if count >= maxScenesPerHome {
			return ErrSceneLimit
		}
		id, err := uuid.NewV7()
		if err != nil {
			return err
		}
		row, err := queries.CreateScene(ctx, scenedb.CreateSceneParams{
			ID: id, HomeID: homeID, Icon: clean.Icon, PresetKey: clean.PresetKey, Name: clean.Name, Actions: actions,
		})
		if err != nil {
			return err
		}
		scene, err = fromRow(row.ID, row.Icon, row.PresetKey, row.Name, row.Actions, row.LastRunAt)
		return err
	})
	if err != nil {
		return Scene{}, err
	}
	return scene, nil
}

func (s *Service) Update(ctx context.Context, userID, homeID, sceneID uuid.UUID, input Input) (Scene, error) {
	if err := s.access.RequireRole(ctx, userID, homeID, home.RoleAdmin); err != nil {
		return Scene{}, err
	}
	clean, actions, err := prepare(input)
	if err != nil {
		return Scene{}, err
	}
	row, err := s.queries.UpdateScene(ctx, scenedb.UpdateSceneParams{
		ID: sceneID, HomeID: homeID, Icon: clean.Icon, PresetKey: clean.PresetKey, Name: clean.Name, Actions: actions,
	})
	if errors.Is(err, pgx.ErrNoRows) {
		return Scene{}, ErrSceneMissing
	}
	if err != nil {
		return Scene{}, err
	}
	return fromRow(row.ID, row.Icon, row.PresetKey, row.Name, row.Actions, row.LastRunAt)
}

func (s *Service) Delete(ctx context.Context, userID, homeID, sceneID uuid.UUID) error {
	if err := s.access.RequireRole(ctx, userID, homeID, home.RoleAdmin); err != nil {
		return err
	}
	removed, err := s.queries.DeleteScene(ctx, scenedb.DeleteSceneParams{ID: sceneID, HomeID: homeID})
	if err != nil {
		return err
	}
	if removed == 0 {
		return ErrSceneMissing
	}
	return nil
}

// RecordRun notes that a member ran the scene. Any member may: scenes are for
// everyone in the home.
func (s *Service) RecordRun(ctx context.Context, userID, homeID, sceneID uuid.UUID) (Scene, error) {
	if err := s.access.RequireRole(ctx, userID, homeID, home.RoleMember); err != nil {
		return Scene{}, err
	}
	row, err := s.queries.MarkSceneRun(ctx, scenedb.MarkSceneRunParams{ID: sceneID, HomeID: homeID})
	if errors.Is(err, pgx.ErrNoRows) {
		return Scene{}, ErrSceneMissing
	}
	if err != nil {
		return Scene{}, err
	}
	return fromRow(row.ID, row.Icon, row.PresetKey, row.Name, row.Actions, row.LastRunAt)
}

// SeedHome gives a new home the default scenes, in the transaction creating it.
func (s *Service) SeedHome(ctx context.Context, tx pgx.Tx, homeID uuid.UUID) error {
	queries := scenedb.New(tx)
	for _, input := range defaultScenes() {
		id, err := uuid.NewV7()
		if err != nil {
			return err
		}
		actions, err := json.Marshal(input.Actions)
		if err != nil {
			return err
		}
		_, err = queries.CreateScene(ctx, scenedb.CreateSceneParams{
			ID: id, HomeID: homeID, Icon: input.Icon, PresetKey: input.PresetKey, Actions: actions,
		})
		if err != nil {
			return err
		}
	}
	return nil
}

func defaultScenes() []Input {
	on, off := true, false
	light := func(target Target, isOn *bool, brightness *float64, temperature *int) Action {
		return Action{Type: actionLight, Target: &target, IsOn: isOn, Brightness: brightness, Temperature: temperature}
	}
	zone := func(key string) Target { return Target{Kind: targetZone, Key: &key} }
	level := func(value float64) *float64 { return &value }
	kelvin := func(value int) *int { return &value }
	preset := func(key string) *string { return &key }
	lockCheck := Action{Type: actionLockCheck}
	return []Input{
		{Icon: "moon", PresetKey: preset(presetSleep), Actions: []Action{
			light(zone("Bedroom"), &on, level(.1), kelvin(2700)),
			light(Target{Kind: targetOthers}, &off, nil, nil),
			lockCheck,
		}},
		{Icon: "home", PresetKey: preset(presetWelcomeHome), Actions: []Action{
			light(zone("Living room"), &on, level(.8), kelvin(3000)),
			light(zone("Front yard"), &on, nil, nil),
		}},
		{Icon: "leave", PresetKey: preset(presetLeaveHome), Actions: []Action{
			light(Target{Kind: targetAll}, &off, nil, nil),
			lockCheck,
		}},
		{Icon: "sunrise", PresetKey: preset(presetWakeUp), Actions: []Action{
			light(zone("Bedroom"), &on, level(.6), kelvin(4500)),
		}},
	}
}

// prepare validates input and returns it cleaned, with its actions as JSON.
func prepare(input Input) (Input, []byte, error) {
	if !slices.Contains(icons, input.Icon) || (input.PresetKey == nil) == (input.Name == nil) {
		return Input{}, nil, ErrInvalidScene
	}
	if input.PresetKey != nil && !slices.Contains(presetKeys, *input.PresetKey) {
		return Input{}, nil, ErrInvalidScene
	}
	if input.Name != nil {
		name := strings.TrimSpace(*input.Name)
		if !home.ValidName(name) {
			return Input{}, nil, ErrInvalidScene
		}
		input.Name = &name
	}
	if len(input.Actions) > maxActionsPerRun {
		return Input{}, nil, ErrInvalidScene
	}
	if input.Actions == nil {
		input.Actions = []Action{}
	}
	if slices.ContainsFunc(input.Actions, invalidAction) {
		return Input{}, nil, ErrInvalidScene
	}
	actions, err := json.Marshal(input.Actions)
	if err != nil {
		return Input{}, nil, err
	}
	return input, actions, nil
}

func invalidAction(action Action) bool {
	switch action.Type {
	case actionLockCheck:
		return action.Target != nil || action.IsOn != nil || action.Brightness != nil || action.Temperature != nil
	case actionLight:
		return action.Target == nil || action.IsOn == nil || !validTarget(*action.Target) ||
			(action.Brightness != nil && (*action.Brightness < minBrightness || *action.Brightness > maxBrightness)) ||
			(action.Temperature != nil && (*action.Temperature < minTemperature || *action.Temperature > maxTemperature))
	}
	return true
}

func validTarget(target Target) bool {
	switch target.Kind {
	case targetDevice, targetZone:
		return target.Key != nil && *target.Key != "" && len(*target.Key) <= maxTargetKey
	case targetOthers, targetAll:
		return target.Key == nil
	}
	return false
}

func fromRow(id uuid.UUID, icon string, presetKey, name *string, actions []byte, lastRunAt *time.Time) (Scene, error) {
	scene := Scene{ID: id, Icon: icon, PresetKey: presetKey, Name: name, LastRunAt: lastRunAt}
	if err := json.Unmarshal(actions, &scene.Actions); err != nil {
		return Scene{}, err
	}
	return scene, nil
}
