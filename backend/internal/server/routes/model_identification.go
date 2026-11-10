package routes

import (
	"github.com/Wei-Shaw/sub2api/internal/handler"
	"github.com/gin-gonic/gin"
)

func registerModelIdentificationRoutes(admin *gin.RouterGroup, h *handler.Handlers) {
	admin.GET("/model-identification/source", h.ModelIdentification.Source)
	admin.PUT("/model-identification/source", h.ModelIdentification.SaveSource)
	plans := admin.Group("/model-identification/plans")
	plans.GET("/:id/results", h.ModelIdentification.AdminResults)
	plans.GET("/:id", h.ModelIdentification.Config)
	plans.POST("", h.ModelIdentification.Create)
	plans.PUT("/:id", h.ModelIdentification.Update)
	plans.DELETE("/:id", h.ModelIdentification.Delete)
	plans.POST("/:id/run", h.ModelIdentification.Run)
}

func registerModelIdentificationReadRoutes(public *gin.RouterGroup, h *handler.Handlers) {
	shared := public.Group("/model-identification")
	shared.GET("/source", h.ModelIdentification.Source)
	shared.GET("/groups", h.ModelIdentification.List)
	shared.GET("/plans/:id/results", h.ModelIdentification.Results)
}
