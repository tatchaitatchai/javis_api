package scene

import (
	"context"
	"errors"

	"javis_api/internal/auth"
	"javis_api/internal/home"
	"javis_api/internal/httpapi/openapi"
)

// Handler implements the scene operations of the contract.
type Handler struct {
	service *Service
}

func NewHandler(service *Service) *Handler {
	return &Handler{service: service}
}

func invalidScene() openapi.ErrorResponse {
	return openapi.NewError("invalid_scene", "Scene details are not valid")
}

func sceneNotFound() openapi.ErrorResponse {
	return openapi.NewError("scene_not_found", "This scene is no longer available")
}

func (h *Handler) ListScenes(ctx context.Context, request openapi.ListScenesRequestObject) (openapi.ListScenesResponseObject, error) {
	principal, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return openapi.ListScenes401JSONResponse(openapi.Unauthorized()), nil
	}
	scenes, err := h.service.List(ctx, principal.UserID, request.HomeId)
	if errors.Is(err, home.ErrHomeNotFound) {
		return openapi.ListScenes404JSONResponse(openapi.HomeNotFound()), nil
	}
	if err != nil {
		return nil, err
	}
	items := make([]openapi.Scene, 0, len(scenes))
	for _, scene := range scenes {
		items = append(items, toAPIScene(scene))
	}
	return openapi.ListScenes200JSONResponse{Items: items}, nil
}

func (h *Handler) CreateScene(ctx context.Context, request openapi.CreateSceneRequestObject) (openapi.CreateSceneResponseObject, error) {
	principal, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return openapi.CreateScene401JSONResponse(openapi.Unauthorized()), nil
	}
	scene, err := h.service.Create(ctx, principal.UserID, request.HomeId, toInput(*request.Body))
	switch {
	case err == nil:
		return openapi.CreateScene201JSONResponse(toAPIScene(scene)), nil
	case errors.Is(err, ErrInvalidScene):
		return openapi.CreateScene400JSONResponse(invalidScene()), nil
	case errors.Is(err, home.ErrForbidden):
		return openapi.CreateScene403JSONResponse(openapi.Forbidden()), nil
	case errors.Is(err, home.ErrHomeNotFound):
		return openapi.CreateScene404JSONResponse(openapi.HomeNotFound()), nil
	case errors.Is(err, ErrSceneLimit):
		return openapi.CreateScene409JSONResponse(openapi.NewError("scene_limit_reached", "This home has the maximum number of scenes")), nil
	}
	return nil, err
}

func (h *Handler) UpdateScene(ctx context.Context, request openapi.UpdateSceneRequestObject) (openapi.UpdateSceneResponseObject, error) {
	principal, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return openapi.UpdateScene401JSONResponse(openapi.Unauthorized()), nil
	}
	scene, err := h.service.Update(ctx, principal.UserID, request.HomeId, request.SceneId, toInput(*request.Body))
	switch {
	case err == nil:
		return openapi.UpdateScene200JSONResponse(toAPIScene(scene)), nil
	case errors.Is(err, ErrInvalidScene):
		return openapi.UpdateScene400JSONResponse(invalidScene()), nil
	case errors.Is(err, home.ErrForbidden):
		return openapi.UpdateScene403JSONResponse(openapi.Forbidden()), nil
	case errors.Is(err, home.ErrHomeNotFound):
		return openapi.UpdateScene404JSONResponse(openapi.HomeNotFound()), nil
	case errors.Is(err, ErrSceneMissing):
		return openapi.UpdateScene404JSONResponse(sceneNotFound()), nil
	}
	return nil, err
}

func (h *Handler) DeleteScene(ctx context.Context, request openapi.DeleteSceneRequestObject) (openapi.DeleteSceneResponseObject, error) {
	principal, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return openapi.DeleteScene401JSONResponse(openapi.Unauthorized()), nil
	}
	err := h.service.Delete(ctx, principal.UserID, request.HomeId, request.SceneId)
	switch {
	case err == nil:
		return openapi.DeleteScene204Response{}, nil
	case errors.Is(err, home.ErrForbidden):
		return openapi.DeleteScene403JSONResponse(openapi.Forbidden()), nil
	case errors.Is(err, home.ErrHomeNotFound):
		return openapi.DeleteScene404JSONResponse(openapi.HomeNotFound()), nil
	case errors.Is(err, ErrSceneMissing):
		return openapi.DeleteScene404JSONResponse(sceneNotFound()), nil
	}
	return nil, err
}

func (h *Handler) RecordSceneRun(ctx context.Context, request openapi.RecordSceneRunRequestObject) (openapi.RecordSceneRunResponseObject, error) {
	principal, ok := auth.PrincipalFrom(ctx)
	if !ok {
		return openapi.RecordSceneRun401JSONResponse(openapi.Unauthorized()), nil
	}
	scene, err := h.service.RecordRun(ctx, principal.UserID, request.HomeId, request.SceneId)
	switch {
	case err == nil:
		return openapi.RecordSceneRun200JSONResponse(toAPIScene(scene)), nil
	case errors.Is(err, home.ErrHomeNotFound):
		return openapi.RecordSceneRun404JSONResponse(openapi.HomeNotFound()), nil
	case errors.Is(err, ErrSceneMissing):
		return openapi.RecordSceneRun404JSONResponse(sceneNotFound()), nil
	}
	return nil, err
}

func toInput(body openapi.SceneInput) Input {
	input := Input{Icon: string(body.Icon), PresetKey: body.PresetKey, Name: body.Name}
	for _, action := range body.Actions {
		item := Action{
			Type:        string(action.Type),
			IsOn:        action.IsOn,
			Brightness:  action.Brightness,
			Temperature: action.Temperature,
		}
		if action.Target != nil {
			item.Target = &Target{Kind: string(action.Target.Kind), Key: action.Target.Key}
		}
		input.Actions = append(input.Actions, item)
	}
	return input
}

func toAPIScene(scene Scene) openapi.Scene {
	actions := make([]openapi.SceneAction, 0, len(scene.Actions))
	for _, action := range scene.Actions {
		item := openapi.SceneAction{
			Type:        openapi.SceneActionType(action.Type),
			IsOn:        action.IsOn,
			Brightness:  action.Brightness,
			Temperature: action.Temperature,
		}
		if action.Target != nil {
			item.Target = &openapi.SceneTarget{Kind: openapi.SceneTargetKind(action.Target.Kind), Key: action.Target.Key}
		}
		actions = append(actions, item)
	}
	return openapi.Scene{
		Id:        scene.ID,
		Icon:      scene.Icon,
		PresetKey: scene.PresetKey,
		Name:      scene.Name,
		Actions:   actions,
		LastRunAt: scene.LastRunAt,
	}
}
