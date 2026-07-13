package agents

import (
	"context"
	"log"
	"os"

	"google.golang.org/adk/v2/agent"
	"google.golang.org/adk/v2/agent/llmagent"
	"google.golang.org/adk/v2/model"
	"google.golang.org/adk/v2/tool"
	"google.golang.org/adk/v2/tool/functiontool"
	"google.golang.org/adk/v2/tool/loadartifactstool"
	"google.golang.org/genai"
)

func generateImage(ctx agent.Context, input generateImageInput) (generateImageResult, error) {
	client, err := genai.NewClient(ctx, &genai.ClientConfig{
		Project:  os.Getenv("GOOGLE_CLOUD_PROJECT"),
		Location: os.Getenv("GOOGLE_CLOUD_LOCATION"),
		Backend:  genai.BackendVertexAI,
	})
	if err != nil {
		return generateImageResult{
			Status: "fail",
		}, nil
	}

	response, err := client.Models.GenerateImages(
		ctx,
		"imagen-3.0-generate-002",
		input.Prompt,
		&genai.GenerateImagesConfig{NumberOfImages: 1})
	if err != nil {
		return generateImageResult{
			Status: "fail",
		}, nil
	}

	_, err = ctx.Artifacts().Save(ctx, input.Filename, genai.NewPartFromBytes(response.GeneratedImages[0].Image.ImageBytes, "image/png"))
	if err != nil {
		return generateImageResult{
			Status: "fail",
		}, nil
	}

	return generateImageResult{
		Status:   "success",
		Filename: input.Filename,
	}, nil
}

type generateImageInput struct {
	Prompt   string `json:"prompt"`
	Filename string `json:"filename"`
}

type generateImageResult struct {
	Filename string `json:"filename"`
	Status   string `json:"Status"`
}

func GetImageGeneratorAgent(ctx context.Context, model model.LLM) agent.Agent {
	generateImageTool, err := functiontool.New(
		functiontool.Config{
			Name:        "generate_image",
			Description: "Generates image and saves in artifact service.",
		},
		generateImage)
	if err != nil {
		log.Fatalf("Failed to create generate image tool: %v", err)
	}
	imageGeneratorAgent, err := llmagent.New(llmagent.Config{
		Name:        "image_generator",
		Model:       model,
		Description: "Agent to generate pictures, answers questions about it and saves it locally if asked.",
		Instruction: "You are an agent whose job is to generate or edit an image based on the user's prompt.",
		Tools: []tool.Tool{
			generateImageTool, loadartifactstool.New(),
		},
	})
	if err != nil {
		log.Fatalf("Failed to create agent: %v", err)
	}
	return imageGeneratorAgent
}
