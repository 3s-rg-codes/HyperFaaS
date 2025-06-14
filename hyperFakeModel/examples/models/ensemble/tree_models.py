"""
Ensemble models for HyperFaaS resource prediction.
Includes Random Forest and Gradient Boosting variants.
"""

import numpy as np
import pandas as pd
from sklearn.ensemble import RandomForestRegressor, GradientBoostingRegressor
from sklearn.metrics import mean_squared_error, mean_absolute_error, r2_score
import joblib
from pathlib import Path
from typing import Dict, List, Optional, Any

import sys
sys.path.append('../../src/models')
from base_model import BaseModel


class RandomForestModel(BaseModel):
    """Random Forest Regressor for resource prediction."""
    
    def __init__(self, config: Dict[str, Any]):
        super().__init__("RandomForest", config)
        self.model = None
        
    def build_model(self) -> None:
        """Build the random forest model."""
        hyperparams = self.config.get('hyperparameters', {})
        
        # Set random state for reproducibility
        if 'random_state' not in hyperparams:
            hyperparams['random_state'] = 42
            
        self.model = RandomForestRegressor(**hyperparams)
        
    def train(self, X_train: np.ndarray, y_train: np.ndarray, 
              X_val: Optional[np.ndarray] = None, y_val: Optional[np.ndarray] = None) -> Dict[str, List[float]]:
        """Train the random forest model."""
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
            'train_r2': [float(np.mean(train_r2))],
            'feature_importance': self.model.feature_importances_.tolist()
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
    
    def get_feature_importance(self) -> Optional[np.ndarray]:
        """Get feature importance from the trained model."""
        if self.is_trained and self.model is not None:
            return self.model.feature_importances_
        return None
    
    def _save_model_specific(self, save_path: Path) -> None:
        """Save the sklearn model."""
        joblib.dump(self.model, save_path / 'model.pkl')
    
    def _load_model_specific(self, load_path: Path) -> None:
        """Load the sklearn model."""
        self.model = joblib.load(load_path / 'model.pkl')


class GradientBoostingModel(BaseModel):
    """Gradient Boosting Regressor for resource prediction."""
    
    def __init__(self, config: Dict[str, Any]):
        super().__init__("GradientBoosting", config)
        self.model = None
        
    def build_model(self) -> None:
        """Build the gradient boosting model."""
        hyperparams = self.config.get('hyperparameters', {})
        
        # Set random state for reproducibility
        if 'random_state' not in hyperparams:
            hyperparams['random_state'] = 42
            
        self.model = GradientBoostingRegressor(**hyperparams)
        
    def train(self, X_train: np.ndarray, y_train: np.ndarray, 
              X_val: Optional[np.ndarray] = None, y_val: Optional[np.ndarray] = None) -> Dict[str, List[float]]:
        """Train the gradient boosting model."""
        if self.model is None:
            self.build_model()
        
        # Note: Gradient Boosting in sklearn doesn't support multi-output directly
        # We'll need to train separate models for each output
        if y_train.ndim > 1 and y_train.shape[1] > 1:
            # Multi-output case - train separate models
            self.models = []
            histories = []
            
            for i in range(y_train.shape[1]):
                model = GradientBoostingRegressor(**self.config.get('hyperparameters', {}))
                model.fit(X_train, y_train[:, i])
                self.models.append(model)
                
                # Calculate metrics for this output
                train_pred = model.predict(X_train)
                train_mse = mean_squared_error(y_train[:, i], train_pred)
                train_mae = mean_absolute_error(y_train[:, i], train_pred)
                train_r2 = r2_score(y_train[:, i], train_pred)
                
                output_history = {
                    f'train_mse_output_{i}': [float(train_mse)],
                    f'train_mae_output_{i}': [float(train_mae)],
                    f'train_r2_output_{i}': [float(train_r2)],
                    f'feature_importance_output_{i}': model.feature_importances_.tolist()
                }
                
                # Validation metrics
                if X_val is not None and y_val is not None:
                    val_pred = model.predict(X_val)
                    val_mse = mean_squared_error(y_val[:, i], val_pred)
                    val_mae = mean_absolute_error(y_val[:, i], val_pred)
                    val_r2 = r2_score(y_val[:, i], val_pred)
                    
                    output_history.update({
                        f'val_mse_output_{i}': [float(val_mse)],
                        f'val_mae_output_{i}': [float(val_mae)],
                        f'val_r2_output_{i}': [float(val_r2)]
                    })
                
                histories.append(output_history)
            
            self.is_trained = True
            
            # Combine histories
            history = {}
            for h in histories:
                history.update(h)
                
            # Calculate overall metrics
            train_pred_all = self.predict(X_train)
            overall_train_mse = mean_squared_error(y_train, train_pred_all, multioutput='raw_values')
            overall_train_mae = mean_absolute_error(y_train, train_pred_all, multioutput='raw_values')
            overall_train_r2 = r2_score(y_train, train_pred_all, multioutput='raw_values')
            
            history.update({
                'train_mse': [float(np.mean(overall_train_mse))],
                'train_mae': [float(np.mean(overall_train_mae))],
                'train_r2': [float(np.mean(overall_train_r2))]
            })
            
            if X_val is not None and y_val is not None:
                val_pred_all = self.predict(X_val)
                overall_val_mse = mean_squared_error(y_val, val_pred_all, multioutput='raw_values')
                overall_val_mae = mean_absolute_error(y_val, val_pred_all, multioutput='raw_values')
                overall_val_r2 = r2_score(y_val, val_pred_all, multioutput='raw_values')
                
                history.update({
                    'val_mse': [float(np.mean(overall_val_mse))],
                    'val_mae': [float(np.mean(overall_val_mae))],
                    'val_r2': [float(np.mean(overall_val_r2))]
                })
            
        else:
            # Single output case
            self.model.fit(X_train, y_train.ravel() if y_train.ndim > 1 else y_train)
            self.is_trained = True
            
            # Calculate training metrics
            train_pred = self.model.predict(X_train)
            train_mse = mean_squared_error(y_train.ravel() if y_train.ndim > 1 else y_train, train_pred)
            train_mae = mean_absolute_error(y_train.ravel() if y_train.ndim > 1 else y_train, train_pred)
            train_r2 = r2_score(y_train.ravel() if y_train.ndim > 1 else y_train, train_pred)
            
            history = {
                'train_mse': [float(train_mse)],
                'train_mae': [float(train_mae)],
                'train_r2': [float(train_r2)],
                'feature_importance': self.model.feature_importances_.tolist()
            }
            
            # Validation metrics if provided
            if X_val is not None and y_val is not None:
                val_pred = self.model.predict(X_val)
                val_mse = mean_squared_error(y_val.ravel() if y_val.ndim > 1 else y_val, val_pred)
                val_mae = mean_absolute_error(y_val.ravel() if y_val.ndim > 1 else y_val, val_pred)
                val_r2 = r2_score(y_val.ravel() if y_val.ndim > 1 else y_val, val_pred)
                
                history.update({
                    'val_mse': [float(val_mse)],
                    'val_mae': [float(val_mae)],
                    'val_r2': [float(val_r2)]
                })
        
        self.training_history = history
        return history
        
    def predict(self, X: np.ndarray) -> np.ndarray:
        """Make predictions with the trained model."""
        if not self.is_trained:
            raise ValueError("Model must be trained before making predictions")
            
        if hasattr(self, 'models'):
            # Multi-output case
            predictions = []
            for model in self.models:
                pred = model.predict(X)
                predictions.append(pred)
            return np.column_stack(predictions)
        else:
            # Single output case
            if self.model is None:
                raise ValueError("Model is not available")
            pred = self.model.predict(X)
            return pred.reshape(-1, 1) if pred.ndim == 1 else pred
    
    def get_feature_importance(self) -> Optional[np.ndarray]:
        """Get feature importance from the trained model."""
        if not self.is_trained:
            return None
            
        if hasattr(self, 'models'):
            # Multi-output case - return average importance
            importances = [model.feature_importances_ for model in self.models]
            return np.mean(importances, axis=0)
        else:
            if self.model is not None:
                return self.model.feature_importances_
        return None
    
    def _save_model_specific(self, save_path: Path) -> None:
        """Save the sklearn model(s)."""
        if hasattr(self, 'models'):
            # Multi-output case
            joblib.dump(self.models, save_path / 'models.pkl')
        else:
            joblib.dump(self.model, save_path / 'model.pkl')
    
    def _load_model_specific(self, load_path: Path) -> None:
        """Load the sklearn model(s)."""
        if (load_path / 'models.pkl').exists():
            # Multi-output case
            self.models = joblib.load(load_path / 'models.pkl')
        else:
            self.model = joblib.load(load_path / 'model.pkl') 