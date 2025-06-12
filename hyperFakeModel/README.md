<<<<<<< HEAD
# HyperFaaS Resource Prediction Models

A comprehensive machine learning framework for predicting serverless function resource usage (now only CPU and memory) based on function call patterns and system metrics.

## Available Models

### Regression Models
- **Linear Regression**: Simple linear relationship modeling
- **Polynomial Regression**: Non-linear relationships with polynomial features
- **Ridge Regression**: L2 regularized linear regression
- **Lasso Regression**: L1 regularized linear regression with feature selection

### Neural Network Models
- **Simple Neural Network**: Basic feedforward network (2-3 hidden layers)
- **Deep Neural Network**: Deeper architecture with batch normalization

### Ensemble Models
- **Random Forest**: Ensemble of decision trees with bagging
- **Gradient Boosting**: Boosted ensemble with sequential learning

## Quick Start

### 1. Install Dependencies

```bash
uv sync
```

### 2. Prepare Your Data

Place your metrics data in the appropriate folder:
- Raw data: `data/raw/metrics.csv`
- Process your data with `./src/data_processing/process.py`

Expected data format:
```csv
timestamp,hyperfaas-thumbnailer-json:latest_active_calls,hyperfaas-bfs-json:latest_active_calls,hyperfaas-echo:latest_active_calls,cpu_usage,memory_usage
2024-01-01T10:00:00,5,3,2,45.2,1024.5
2024-01-01T10:01:00,7,2,4,52.1,1156.3
...
```

### 3. Configure Models

Edit `configs/model_config.yaml` to customize:
- Model hyperparameters
- Data paths
- Training settings
- Evaluation metrics

### 4. Train All Models

```bash
uv run python src/train_models.py
```

### 5. View Results

After training, check:
- `experiments/results/` for training results and plots
- `experiments/results/trained_models_*/` for saved models
- `experiments/results/model_comparison_*.csv` for performance comparison
=======
# hyperFake Model

## Training Data (last updated: 06.06.2025)

### Input

- Request Body size
- The number of function instances (number of containers that could potentially execute client code)
- The number of active function calls (number of containers in which user code is actively executing a call)
- Worker resource consumption

**Worker resource consumption** is the sum of the resource consumption of all running functions.

### Output

- Function runtime
- Function resource consumption (average throughout execution)

## Training Data Format

Each row should contain the information for one function call.

| Parameter | Body Size | Number of Function Instances | Number of active Function Calls | Worker CPU Usage | Worker RAM Usage | Function Runtime | Function CPU Usage | Function RAM Usage |
| --------- | --------- | ---------------------------- | ------------------------------- | ---------------- | ---------------- | ---------------- | ------------------ | ------------------ |
| Unit      | Byte      | Count                        | Count                           | Cores            | Bytes            | Nanoseconds      | Cores              | Bytes              |
| Type      | Int64     | Int64                        | Int64                           | Float64          | Int64            | Int64            | Float64            | Int64              |
>>>>>>> 2c5e3bf0edd701204c00a1b4dfd0fa24bee656ff
