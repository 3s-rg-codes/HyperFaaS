"""
Linear regression models for HyperFaaS resource prediction.
Includes Linear, Polynomial, Ridge, and Lasso regression variants.
"""

import numpy as np
import pandas as pd
from sklearn.linear_model import LinearRegression, Ridge, Lasso
from sklearn.preprocessing import PolynomialFeatures
from sklearn.pipeline import Pipeline
from sklearn.metrics import mean_squared_error, mean_absolute_error, r2_score
import joblib
from pathlib import Path
from typing import Dict, List, Optional, Any

import sys
sys.path.append('../../src/models')
from base_model import BaseModel


class LinearRegressionModel(BaseModel):
    """Simple Linear Regression for resource prediction."""
    
    def __init__(self, config: Dict[str, Any]):
        super().__init__("LinearRegression", config)
        self.model = None
        
    def build_model(self) -> None:
        """Build the linear regression model."""
        self.model = LinearRegression(**self.config.get('hyperparameters', {}))
        
    def train(self, X_train: np.ndarray, y_train: np.ndarray, 
              X_val: Optional[np.ndarray] = None, y_val: Optional[np.ndarray] = None) -> Dict[str, List[float]]:
        """Train the linear regression model."""
        if self.model is None:
            self.build_model()
            
        # Fit the model
        self.model.fit(X_train, y_train)
        self.is_trained = True
        
        # Calculate training metrics
        train_pred = self.model.predict(X_train)
        train_mse = mean_squared_error(y_train, train_pred, multioutput='raw_values')
        train_mae = mean_absolute_error(y_train, train_pred, multioutput='raw_values')
        train_r2 = r2_score(y_train, train_pred, multioutput='raw_values')
        
        history = {
            'train_mse': [float(np.mean(train_mse))],
            'train_mae': [float(np.mean(train_mae))],
            'train_r2': [float(np.mean(train_r2))]
        }
        
        # Validation metrics if provided
        if X_val is not None and y_val is not None:
            val_pred = self.model.predict(X_val)
            val_mse = mean_squared_error(y_val, val_pred, multioutput='raw_values')
            val_mae = mean_absolute_error(y_val, val_pred, multioutput='raw_values')
            val_r2 = r2_score(y_val, val_pred, multioutput='raw_values')
            
            history.update({
                'val_mse': [float(np.mean(val_mse))],
                'val_mae': [float(np.mean(val_mae))],
                'val_r2': [float(np.mean(val_r2))]
            })
        
        self.training_history = history
        return history
        
    def predict(self, X: np.ndarray) -> np.ndarray:
        """Make predictions with the trained model."""
        if not self.is_trained or self.model is None:
            raise ValueError("Model must be trained before making predictions")
        return self.model.predict(X)
    
    def _save_model_specific(self, save_path: Path) -> None:
        """Save the sklearn model."""
        joblib.dump(self.model, save_path / 'model.pkl')
    
    def _load_model_specific(self, load_path: Path) -> None:
        """Load the sklearn model."""
        self.model = joblib.load(load_path / 'model.pkl')


class PolynomialRegressionModel(BaseModel):
    """Polynomial Regression for resource prediction."""
    
    def __init__(self, config: Dict[str, Any]):
        super().__init__("PolynomialRegression", config)
        self.model = None
        
    def build_model(self) -> None:
        """Build the polynomial regression model."""
        degree = self.config.get('hyperparameters', {}).get('degree', 2)
        fit_intercept = self.config.get('hyperparameters', {}).get('fit_intercept', True)
        
        self.model = Pipeline([
            ('poly', PolynomialFeatures(degree=degree, include_bias=fit_intercept)),
            ('linear', LinearRegression(fit_intercept=fit_intercept))
        ])
        
    def train(self, X_train: np.ndarray, y_train: np.ndarray, 
              X_val: Optional[np.ndarray] = None, y_val: Optional[np.ndarray] = None) -> Dict[str, List[float]]:
        """Train the polynomial regression model."""
        if self.model is None:
            self.build_model()
            
        # Fit the model
        self.model.fit(X_train, y_train)
        self.is_trained = True
        
        # Calculate training metrics
        train_pred = self.model.predict(X_train)
        train_mse = mean_squared_error(y_train, train_pred, multioutput='raw_values')
        train_mae = mean_absolute_error(y_train, train_pred, multioutput='raw_values')
        train_r2 = r2_score(y_train, train_pred, multioutput='raw_values')
        
        history = {
            'train_mse': [float(np.mean(train_mse))],
            'train_mae': [float(np.mean(train_mae))],
            'train_r2': [float(np.mean(train_r2))]
        }
        
        # Validation metrics if provided
        if X_val is not None and y_val is not None:
            val_pred = self.model.predict(X_val)
            val_mse = mean_squared_error(y_val, val_pred, multioutput='raw_values')
            val_mae = mean_absolute_error(y_val, val_pred, multioutput='raw_values')
            val_r2 = r2_score(y_val, val_pred, multioutput='raw_values')
            
            history.update({
                'val_mse': [float(np.mean(val_mse))],
                'val_mae': [float(np.mean(val_mae))],
                'val_r2': [float(np.mean(val_r2))]
            })
        
        self.training_history = history
        return history
        
    def predict(self, X: np.ndarray) -> np.ndarray:
        """Make predictions with the trained model."""
        if not self.is_trained or self.model is None:
            raise ValueError("Model must be trained before making predictions")
        return self.model.predict(X)
    
    def _save_model_specific(self, save_path: Path) -> None:
        """Save the sklearn model."""
        joblib.dump(self.model, save_path / 'model.pkl')
    
    def _load_model_specific(self, load_path: Path) -> None:
        """Load the sklearn model."""
        self.model = joblib.load(load_path / 'model.pkl')


class RidgeRegressionModel(BaseModel):
    """Ridge Regression with L2 regularization for resource prediction."""
    
    def __init__(self, config: Dict[str, Any]):
        super().__init__("RidgeRegression", config)
        self.model = None
        
    def build_model(self) -> None:
        """Build the ridge regression model."""
        self.model = Ridge(**self.config.get('hyperparameters', {}))
        
    def train(self, X_train: np.ndarray, y_train: np.ndarray, 
              X_val: Optional[np.ndarray] = None, y_val: Optional[np.ndarray] = None) -> Dict[str, List[float]]:
        """Train the ridge regression model."""
        if self.model is None:
            self.build_model()
            
        # Fit the model
        self.model.fit(X_train, y_train)
        self.is_trained = True
        
        # Calculate training metrics
        train_pred = self.model.predict(X_train)
        train_mse = mean_squared_error(y_train, train_pred, multioutput='raw_values')
        train_mae = mean_absolute_error(y_train, train_pred, multioutput='raw_values')
        train_r2 = r2_score(y_train, train_pred, multioutput='raw_values')
        
        history = {
            'train_mse': [float(np.mean(train_mse))],
            'train_mae': [float(np.mean(train_mae))],
            'train_r2': [float(np.mean(train_r2))]
        }
        
        # Validation metrics if provided
        if X_val is not None and y_val is not None:
            val_pred = self.model.predict(X_val)
            val_mse = mean_squared_error(y_val, val_pred, multioutput='raw_values')
            val_mae = mean_absolute_error(y_val, val_pred, multioutput='raw_values')
            val_r2 = r2_score(y_val, val_pred, multioutput='raw_values')
            
            history.update({
                'val_mse': [float(np.mean(val_mse))],
                'val_mae': [float(np.mean(val_mae))],
                'val_r2': [float(np.mean(val_r2))]
            })
        
        self.training_history = history
        return history
        
    def predict(self, X: np.ndarray) -> np.ndarray:
        """Make predictions with the trained model."""
        if not self.is_trained or self.model is None:
            raise ValueError("Model must be trained before making predictions")
        return self.model.predict(X)
    
    def _save_model_specific(self, save_path: Path) -> None:
        """Save the sklearn model."""
        joblib.dump(self.model, save_path / 'model.pkl')
    
    def _load_model_specific(self, load_path: Path) -> None:
        """Load the sklearn model."""
        self.model = joblib.load(load_path / 'model.pkl')


class LassoRegressionModel(BaseModel):
    """Lasso Regression with L1 regularization for resource prediction."""
    
    def __init__(self, config: Dict[str, Any]):
        super().__init__("LassoRegression", config)
        self.model = None
        
    def build_model(self) -> None:
        """Build the lasso regression model."""
        self.model = Lasso(**self.config.get('hyperparameters', {}))
        
    def train(self, X_train: np.ndarray, y_train: np.ndarray, 
              X_val: Optional[np.ndarray] = None, y_val: Optional[np.ndarray] = None) -> Dict[str, List[float]]:
        """Train the lasso regression model."""
        if self.model is None:
            self.build_model()
            
        # Fit the model
        self.model.fit(X_train, y_train)
        self.is_trained = True
        
        # Calculate training metrics
        train_pred = self.model.predict(X_train)
        train_mse = mean_squared_error(y_train, train_pred, multioutput='raw_values')
        train_mae = mean_absolute_error(y_train, train_pred, multioutput='raw_values')
        train_r2 = r2_score(y_train, train_pred, multioutput='raw_values')
        
        history = {
            'train_mse': [float(np.mean(train_mse))],
            'train_mae': [float(np.mean(train_mae))],
            'train_r2': [float(np.mean(train_r2))]
        }
        
        # Validation metrics if provided
        if X_val is not None and y_val is not None:
            val_pred = self.model.predict(X_val)
            val_mse = mean_squared_error(y_val, val_pred, multioutput='raw_values')
            val_mae = mean_absolute_error(y_val, val_pred, multioutput='raw_values')
            val_r2 = r2_score(y_val, val_pred, multioutput='raw_values')
            
            history.update({
                'val_mse': [float(np.mean(val_mse))],
                'val_mae': [float(np.mean(val_mae))],
                'val_r2': [float(np.mean(val_r2))]
            })
        
        self.training_history = history
        return history
        
    def predict(self, X: np.ndarray) -> np.ndarray:
        """Make predictions with the trained model."""
        if not self.is_trained or self.model is None:
            raise ValueError("Model must be trained before making predictions")
        return self.model.predict(X)
    
    def _save_model_specific(self, save_path: Path) -> None:
        """Save the sklearn model."""
        joblib.dump(self.model, save_path / 'model.pkl')
    
    def _load_model_specific(self, load_path: Path) -> None:
        """Load the sklearn model."""
        self.model = joblib.load(load_path / 'model.pkl') 