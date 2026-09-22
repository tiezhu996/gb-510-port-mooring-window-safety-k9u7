package handler

import (
	"errors"
	"net/http"

	"github.com/blueship581/port-mooring-window-safety/backend/internal/dto"
	"github.com/blueship581/port-mooring-window-safety/backend/internal/middleware"
	"github.com/blueship581/port-mooring-window-safety/backend/internal/service"
	"github.com/blueship581/port-mooring-window-safety/backend/internal/util"
	"github.com/gin-gonic/gin"
)

type VesselCallHandler struct {
	service service.VesselCallService
	gate    service.BerthingGateService
}

func NewVesselCallHandler(s service.VesselCallService, gate service.BerthingGateService) *VesselCallHandler {
	return &VesselCallHandler{service: s, gate: gate}
}

func (h *VesselCallHandler) Register(group *gin.RouterGroup) {
	resource := group.Group("/vessels")
	resource.GET("", h.list)
	// 静态路由优先于 /:id，靠泊放行闸门的只读结果端点。
	resource.GET("/gate-checks/latest", h.latestGateChecks)
	resource.GET("/:id", h.get)
	resource.GET("/:id/gate-check", h.gateCheck)
	resource.POST("", middleware.RequireMinimumRole("operator"), h.create)
	resource.PUT("/:id", middleware.RequireMinimumRole("operator"), h.update)
	resource.POST("/:id/transition", middleware.RequireMinimumRole("operator"), h.transition)
	resource.DELETE("/:id", middleware.RequireRoles("admin"), h.remove)
}

func (h *VesselCallHandler) list(c *gin.Context) {
	query := bindPage(c)
	result, err := h.service.List(c.Request.Context(), query)
	if err != nil {
		handleError(c, err)
		return
	}
	util.Page(c, result.Items, result.Page, result.PageSize, result.Total)
}

func (h *VesselCallHandler) get(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	item, err := h.service.Get(c.Request.Context(), id)
	if err != nil {
		handleError(c, err)
		return
	}
	util.OK(c, item)
}

// gateCheck performs a live, non-persisting evaluation of the berthing gate.
func (h *VesselCallHandler) gateCheck(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	result, err := h.gate.Evaluate(c.Request.Context(), id, actorFromContext(c), requestIDFromContext(c))
	if err != nil {
		handleError(c, err)
		return
	}
	util.OK(c, result)
}

// latestGateChecks returns the latest persisted gate result per vessel so the
// berthing page can read results back after a refresh.
func (h *VesselCallHandler) latestGateChecks(c *gin.Context) {
	results, err := h.gate.LatestResults(c.Request.Context())
	if err != nil {
		handleError(c, err)
		return
	}
	util.OK(c, results)
}

func (h *VesselCallHandler) create(c *gin.Context) {
	var input dto.CreateVesselCall
	if err := c.ShouldBindJSON(&input); err != nil {
		util.Fail(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	item, err := h.service.Create(c.Request.Context(), input, actorFromContext(c), requestIDFromContext(c))
	if err != nil {
		handleError(c, err)
		return
	}
	util.Created(c, item)
}

func (h *VesselCallHandler) update(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var input dto.UpdateVesselCall
	if err := c.ShouldBindJSON(&input); err != nil {
		util.Fail(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	item, err := h.service.Update(c.Request.Context(), id, input, actorFromContext(c), requestIDFromContext(c))
	if err != nil {
		handleError(c, err)
		return
	}
	util.OK(c, item)
}

func (h *VesselCallHandler) transition(c *gin.Context) {
	id, ok := parseID(c)
	if !ok {
		return
	}
	var input dto.TransitionRequest
	if err := c.ShouldBindJSON(&input); err != nil {
		util.Fail(c, http.StatusBadRequest, "invalid_request", err.Error())
		return
	}
	item, err := h.service.Transition(c.Request.Context(), id, input, actorFromContext(c), requestIDFromContext(c))
	if err != nil {
		var blocked *service.GateBlockedError
		if errors.As(err, &blocked) {
			util.FailDetailed(c, http.StatusUnprocessableEntity, "berthing_gate_blocked",
				"靠泊放行闸门未通过，任务保持原状态", blocked.Result)
			return
		}
		handleError(c, err)
		return
	}
	util.OK(c, item)
}

func (h *VesselCallHandler) remove(c *gin.Context) {
	if roleFromContext(c) != "admin" {
		util.Fail(c, http.StatusForbidden, "forbidden", "admin role is required")
		return
	}
	id, ok := parseID(c)
	if !ok {
		return
	}
	if err := h.service.Delete(c.Request.Context(), id, actorFromContext(c), requestIDFromContext(c)); err != nil {
		handleError(c, err)
		return
	}
	util.NoContent(c)
}
