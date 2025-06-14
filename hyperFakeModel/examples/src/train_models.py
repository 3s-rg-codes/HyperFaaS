"""
Training pipeline for HyperFaaS resource prediction models.
Handles data loading, preprocessing, model training, and evaluation.
"""
import sys
sys.path.append('src/models')

import numpy as np
import pandas as pd
import yaml
from pathlib import Path
from sklearn.model_selection import train_test_split
from sklearn.preprocessing import StandardScaler
from sklearn.metrics import mean_squared_error, mean_absolute_error, r2_score
import matplotlib.pyplot as plt
import seaborn as sns
from typing import Dict, List, Tuple, Optional
import joblib
import json
from datetime import datetime

# Import our custom modules
import sys
sys.path.append('models')
from model_factory import ModelFactory


class ModelTrainer:
    """Comprehensive training pipeline for resource prediction models."""
    
    def __init__(self, config_path: str):
        """
        Initialize the trainer with configuration.
        
        Args:
            config_path: Path to the YAML configuration file
        """
        with open(config_path, 'r') as f:
            self.config = yaml.safe_load(f)
        
        self.models = {}
        self.results = {}
        self.scaler_X = StandardScaler()
        self.scaler_y = StandardScaler()
        self.X_train = None
        self.X_val = None
        self.X_test = None
        self.y_train = None
        self.y_val = None
        self.y_test = None
        
    def load_and_preprocess_data(self) -> None:
        """Load and preprocess the training data."""
        print("📊 Loading and preprocessing data...")
        
        # Load data
        data_config = self.config.get('data', {})
        data_path = data_config.get('processed_data_path', 'data/processed/training.csv')
        
        if not Path(data_path).exists():
            # Try alternative path
            data_path = data_config.get('raw_data_path', 'data/raw/metrics.csv')
            
        if not Path(data_path).exists():
            raise FileNotFoundError(f"Data file not found: {data_path}")
        
        df = pd.read_csv(data_path)
        print(f"  • Loaded data with shape: {df.shape}")
        
        # Extract features and targets
        feature_columns = data_config.get('feature_columns', [])
        target_columns = data_config.get('target_columns', ['cpu_usage', 'memory_usage'])
        
        # If feature columns not specified, use all numeric columns except targets
        if not feature_columns:
            numeric_columns = df.select_dtypes(include=[np.number]).columns
            feature_columns = [col for col in numeric_columns if col not in target_columns]
        
        # Filter for available columns
        available_features = [col for col in feature_columns if col in df.columns]
        available_targets = [col for col in target_columns if col in df.columns]
        
        if not available_features:
            raise ValueError(f"No feature columns found in data. Available columns: {df.columns.tolist()}")
        
        if not available_targets:
            raise ValueError(f"No target columns found in data. Available columns: {df.columns.tolist()}")
        
        print(f"  • Using {len(available_features)} features: {available_features}")
        print(f"  • Predicting {len(available_targets)} targets: {available_targets}")
        
        # Extract features and targets
        X = df[available_features].values
        y = df[available_targets].values
        
        # Handle missing values
        X = np.nan_to_num(X, nan=0.0)
        y = np.nan_to_num(y, nan=0.0)
        
        # Split data
        training_config = self.config.get('training', {})
        test_size = training_config.get('test_size', 0.2)
        val_size = training_config.get('validation_size', 0.1)
        random_state = training_config.get('random_state', 42)
        
        # First split: train+val vs test
        X_temp, self.X_test, y_temp, self.y_test = train_test_split(
            X, y, test_size=test_size, random_state=random_state
        )
        
        # Second split: train vs val
        val_size_adjusted = val_size / (1 - test_size)
        self.X_train, self.X_val, self.y_train, self.y_val = train_test_split(
            X_temp, y_temp, test_size=val_size_adjusted, random_state=random_state
        )
        
        # Scale features
        self.X_train = self.scaler_X.fit_transform(self.X_train)
        self.X_val = self.scaler_X.transform(self.X_val)
        self.X_test = self.scaler_X.transform(self.X_test)
        
        # Scale targets (optional, can help with neural networks)
        self.y_train_scaled = self.scaler_y.fit_transform(self.y_train)
        self.y_val_scaled = self.scaler_y.transform(self.y_val)
        self.y_test_scaled = self.scaler_y.transform(self.y_test)
        
        print(f"  • Train set: {self.X_train.shape[0]} samples")
        print(f"  • Validation set: {self.X_val.shape[0]} samples")
        print(f"  • Test set: {self.X_test.shape[0]} samples")
        
        # Store feature and target names
        self.feature_names = available_features
        self.target_names = available_targets
        
    def create_models(self) -> None:
        """Create all models from configuration."""
        print("\n🤖 Creating models...")
        
        model_configs = self.config.get('models', {})
        for model_name, model_config in model_configs.items():
            try:
                model = ModelFactory.create_model(model_name, model_config)
                model.feature_names = self.feature_names
                model.target_names = self.target_names
                self.models[model_name] = model
                print(f"  ✓ Created {model_name}")
            except Exception as e:
                print(f"  ✗ Failed to create {model_name}: {str(e)}")
    
    def train_model(self, model_name: str, model) -> Dict:
        """Train a single model and return results."""
        print(f"\n🏋️ Training {model_name}...")
        
        try:
            # Decide whether to use scaled targets (for neural networks)
            use_scaled = 'neural' in model_name.lower()
            
            if use_scaled:
                y_train = self.y_train_scaled
                y_val = self.y_val_scaled
            else:
                y_train = self.y_train
                y_val = self.y_val
            
            # Train the model
            history = model.train(self.X_train, y_train, self.X_val, y_val)
            
            # Make predictions on test set
            test_pred = model.predict(self.X_test)
            
            # If we used scaled targets, inverse transform predictions
            if use_scaled:
                test_pred = self.scaler_y.inverse_transform(test_pred)
            
            # Evaluate on test set
            test_metrics = model.evaluate(self.X_test, self.y_test)
            
            # Calculate additional metrics
            results = {
                'model_name': model_name,
                'training_history': history,
                'test_metrics': test_metrics,
                'test_predictions': test_pred.tolist(),
                'training_time': datetime.now().isoformat(),
                'model_type': model.config.get('type', 'unknown'),
                'hyperparameters': model.config.get('hyperparameters', {}),
                'feature_names': self.feature_names,
                'target_names': self.target_names
            }
            
            # Add feature importance if available
            if hasattr(model, 'get_feature_importance'):
                importance = model.get_feature_importance()
                if importance is not None:
                    results['feature_importance'] = importance.tolist()
            
            print(f"  ✓ Training completed successfully")
            print(f"    • Test MSE: {test_metrics.get('mse_total', 'N/A'):.4f}")
            print(f"    • Test R²: {test_metrics.get('r2_total', 'N/A'):.4f}")
            
            return results
            
        except Exception as e:
            print(f"  ✗ Training failed: {str(e)}")
            return {
                'model_name': model_name,
                'error': str(e),
                'training_time': datetime.now().isoformat()
            }
    
    def train_all_models(self) -> None:
        """Train all created models."""
        print("\n🚀 Starting training pipeline...")
        
        for model_name, model in self.models.items():
            results = self.train_model(model_name, model)
            self.results[model_name] = results
    
    def compare_models(self) -> pd.DataFrame:
        """Compare performance of all trained models."""
        print("\n📊 Comparing model performance...")
        
        comparison_data = []
        
        for model_name, results in self.results.items():
            if 'error' in results:
                continue
                
            test_metrics = results.get('test_metrics', {})
            
            comparison_data.append({
                'Model': model_name,
                'Type': results.get('model_type', 'unknown'),
                'MSE_Total': test_metrics.get('mse_total', np.nan),
                'MAE_Total': test_metrics.get('mae_total', np.nan),
                'R2_Total': test_metrics.get('r2_total', np.nan),
                'MSE_CPU': test_metrics.get('mse_cpu', np.nan),
                'MSE_Memory': test_metrics.get('mse_memory', np.nan),
                'R2_CPU': test_metrics.get('r2_cpu', np.nan),
                'R2_Memory': test_metrics.get('r2_memory', np.nan),
            })
        
        df = pd.DataFrame(comparison_data)
        
        if not df.empty:
            # Sort by R2 score (best first)
            df = df.sort_values('R2_Total', ascending=False)
            
            print("\n🏆 Model Performance Rankings:")
            print("=" * 80)
            print(df.to_string(index=False, float_format='%.4f'))
            
        return df
    
    def save_results(self, output_dir: str = "experiments/results") -> None:
        """Save all training results and models."""
        output_path = Path(output_dir)
        output_path.mkdir(parents=True, exist_ok=True)
        
        timestamp = datetime.now().strftime("%Y%m%d_%H%M%S")
        
        print(f"\n💾 Saving results to {output_path}...")
        
        # Save results as JSON
        results_file = output_path / f"training_results_{timestamp}.json"
        with open(results_file, 'w') as f:
            json.dump(self.results, f, indent=2)
        print(f"  ✓ Results saved to {results_file}")
        
        # Save model comparison
        comparison_df = self.compare_models()
        if not comparison_df.empty:
            csv_file = output_path / f"model_comparison_{timestamp}.csv"
            comparison_df.to_csv(csv_file, index=False)
            print(f"  ✓ Comparison saved to {csv_file}")
        
        # Save trained models
        models_dir = output_path / f"trained_models_{timestamp}"
        models_dir.mkdir(exist_ok=True)
        
        for model_name, model in self.models.items():
            if model.is_trained:
                try:
                    model_path = models_dir / model_name
                    model.save_model(str(model_path))
                    print(f"  ✓ Saved model: {model_name}")
                except Exception as e:
                    print(f"  ✗ Failed to save {model_name}: {str(e)}")
        
        # Save scalers
        scaler_path = models_dir / "scalers"
        scaler_path.mkdir(exist_ok=True)
        joblib.dump(self.scaler_X, scaler_path / "feature_scaler.pkl")
        joblib.dump(self.scaler_y, scaler_path / "target_scaler.pkl")
        print(f"  ✓ Saved scalers")
    
    def plot_results(self, output_dir: str = "experiments/results") -> None:
        """Create visualization plots of the results."""
        if not self.results:
            print("No results to plot")
            return
            
        output_path = Path(output_dir)
        output_path.mkdir(parents=True, exist_ok=True)
        
        # Model comparison plot
        comparison_df = self.compare_models()
        if not comparison_df.empty:
            plt.figure(figsize=(12, 8))
            
            # R2 scores comparison
            plt.subplot(2, 2, 1)
            plt.bar(comparison_df['Model'], comparison_df['R2_Total'])
            plt.title('Model R² Scores')
            plt.xticks(rotation=45)
            plt.ylabel('R² Score')
            
            # MSE comparison
            plt.subplot(2, 2, 2)
            plt.bar(comparison_df['Model'], comparison_df['MSE_Total'])
            plt.title('Model MSE')
            plt.xticks(rotation=45)
            plt.ylabel('MSE')
            
            # CPU vs Memory R2
            plt.subplot(2, 2, 3)
            x = range(len(comparison_df))
            plt.bar([i - 0.2 for i in x], comparison_df['R2_CPU'], width=0.4, label='CPU')
            plt.bar([i + 0.2 for i in x], comparison_df['R2_Memory'], width=0.4, label='Memory')
            plt.title('R² Scores by Resource Type')
            plt.xticks(x, comparison_df['Model'], rotation=45)
            plt.ylabel('R² Score')
            plt.legend()
            
            # Model types distribution
            plt.subplot(2, 2, 4)
            type_counts = comparison_df['Type'].value_counts()
            plt.pie(type_counts.values, labels=type_counts.index, autopct='%1.1f%%')
            plt.title('Model Types Distribution')
            
            plt.tight_layout()
            plot_file = output_path / "model_comparison.png"
            plt.savefig(plot_file, dpi=300, bbox_inches='tight')
            plt.close()
            print(f"  ✓ Comparison plot saved to {plot_file}")
    
    def run_full_pipeline(self) -> None:
        """Run the complete training pipeline."""
        print("🚀 Starting HyperFaaS Resource Prediction Model Training Pipeline")
        print("=" * 70)
        
        try:
            # Step 1: Load and preprocess data
            self.load_and_preprocess_data()
            
            # Step 2: Create models
            self.create_models()
            
            # Step 3: Train all models
            self.train_all_models()
            
            # Step 4: Compare results
            self.compare_models()
            
            # Step 5: Save results
            self.save_results()
            
            # Step 6: Create plots
            self.plot_results()
            
            print("\n🎉 Training pipeline completed successfully!")
            
        except Exception as e:
            print(f"\n❌ Pipeline failed: {str(e)}")
            raise


def main():
    """Main function to run the training pipeline."""
    config_path = "configs/model_config.yaml"
    
    if not Path(config_path).exists():
        print(f"❌ Configuration file not found: {config_path}")
        print("Please create the configuration file first.")
        return
    
    trainer = ModelTrainer(config_path)
    trainer.run_full_pipeline()


if __name__ == "__main__":
    main() 