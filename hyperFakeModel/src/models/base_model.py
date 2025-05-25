"""
Base model class for HyperFaaS resource prediction models.
Provides a consistent interface for all model types.
"""

from abc import ABC, abstractmethod
import numpy as np
import pandas as pd
from typing import Dict, List, Tuple, Optional, Any
import joblib
import json
from pathlib import Path


class BaseModel(ABC):
    """Abstract base class for all resource prediction models."""
    
    def __init__(self, name: str, config: Dict[str, Any]):
        """
        Initialize the base model.
        
        Args:
            name: Name of the model
            config: Configuration dictionary containing hyperparameters
        """
        self.name = name
        self.config = config
        self.is_trained = False
        self.training_history = []
        self.feature_names = None
        self.target_names = None
        
    @abstractmethod
    def build_model(self) -> None:
        """Build the model architecture."""
        pass
    
    @abstractmethod
    def train(self, X_train: np.ndarray, y_train: np.ndarray, 
              X_val: Optional[np.ndarray] = None, y_val: Optional[np.ndarray] = None) -> Dict[str, List[float]]:
        """
        Train the model.
        
        Args:
            X_train: Training features
            y_train: Training targets
            X_val: Validation features (optional)
            y_val: Validation targets (optional)
            
        Returns:
            Training history dictionary
        """
        pass
    
    @abstractmethod
    def predict(self, X: np.ndarray) -> np.ndarray:
        """
        Make predictions.
        
        Args:
            X: Input features
            
        Returns:
            Predictions
        """
        pass
    
    def evaluate(self, X_test: np.ndarray, y_test: np.ndarray) -> Dict[str, float]:
        """
        Evaluate the model on test data.
        
        Args:
            X_test: Test features
            y_test: Test targets
            
        Returns:
            Evaluation metrics
        """
        predictions = self.predict(X_test)
        
        # Calculate various metrics
        metrics = {}
        
        # Mean Squared Error
        mse = np.mean((y_test - predictions) ** 2, axis=0)
        metrics['mse_cpu'] = float(mse[0]) if len(mse) > 1 else float(mse)
        if len(mse) > 1:
            metrics['mse_memory'] = float(mse[1])
        metrics['mse_total'] = float(np.mean(mse))
        
        # Mean Absolute Error
        mae = np.mean(np.abs(y_test - predictions), axis=0)
        metrics['mae_cpu'] = float(mae[0]) if len(mae) > 1 else float(mae)
        if len(mae) > 1:
            metrics['mae_memory'] = float(mae[1])
        metrics['mae_total'] = float(np.mean(mae))
        
        # R² Score
        ss_res = np.sum((y_test - predictions) ** 2, axis=0)
        ss_tot = np.sum((y_test - np.mean(y_test, axis=0)) ** 2, axis=0)
        r2 = 1 - (ss_res / (ss_tot + 1e-8))
        metrics['r2_cpu'] = float(r2[0]) if len(r2) > 1 else float(r2)
        if len(r2) > 1:
            metrics['r2_memory'] = float(r2[1])
        metrics['r2_total'] = float(np.mean(r2))
        
        # Mean Absolute Percentage Error
        mape = np.mean(np.abs((y_test - predictions) / (y_test + 1e-8)) * 100, axis=0)
        metrics['mape_cpu'] = float(mape[0]) if len(mape) > 1 else float(mape)
        if len(mape) > 1:
            metrics['mape_memory'] = float(mape[1])
        metrics['mape_total'] = float(np.mean(mape))
        
        return metrics
    
    def save_model(self, save_path: str) -> None:
        """
        Save the trained model.
        
        Args:
            save_path: Path to save the model
        """
        save_path = Path(save_path)
        save_path.mkdir(parents=True, exist_ok=True)
        
        # Save model-specific components (implemented by subclasses)
        self._save_model_specific(save_path)
        
        # Save metadata
        metadata = {
            'name': self.name,
            'config': self.config,
            'is_trained': self.is_trained,
            'feature_names': self.feature_names,
            'target_names': self.target_names,
            'training_history': self.training_history
        }
        
        with open(save_path / 'metadata.json', 'w') as f:
            json.dump(metadata, f, indent=2)
    
    def load_model(self, load_path: str) -> None:
        """
        Load a trained model.
        
        Args:
            load_path: Path to load the model from
        """
        load_path = Path(load_path)
        
        # Load metadata
        with open(load_path / 'metadata.json', 'r') as f:
            metadata = json.load(f)
        
        self.name = metadata['name']
        self.config = metadata['config']
        self.is_trained = metadata['is_trained']
        self.feature_names = metadata['feature_names']
        self.target_names = metadata['target_names']
        self.training_history = metadata['training_history']
        
        # Load model-specific components
        self._load_model_specific(load_path)
    
    @abstractmethod
    def _save_model_specific(self, save_path: Path) -> None:
        """Save model-specific components (implemented by subclasses)."""
        pass
    
    @abstractmethod
    def _load_model_specific(self, load_path: Path) -> None:
        """Load model-specific components (implemented by subclasses)."""
        pass
    
    def get_model_info(self) -> Dict[str, Any]:
        """Get model information."""
        return {
            'name': self.name,
            'type': self.__class__.__name__,
            'is_trained': self.is_trained,
            'config': self.config,
            'feature_names': self.feature_names,
            'target_names': self.target_names
        } 