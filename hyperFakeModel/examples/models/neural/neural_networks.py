"""
Neural network models for HyperFaaS resource prediction using PyTorch.
Includes Simple NN and Deep NN variants.
"""

import numpy as np
import torch
import torch.nn as nn
import torch.optim as optim
from torch.utils.data import DataLoader, TensorDataset
from pathlib import Path
from typing import Dict, List, Optional, Any
import sys
sys.path.append('../../src/models')
from base_model import BaseModel


class SimpleNeuralNetwork(BaseModel):
    """Simple feedforward neural network for resource prediction."""
    
    def __init__(self, config: Dict[str, Any]):
        super().__init__("SimpleNeuralNetwork", config)
        self.model = None
        self.device = torch.device('cuda' if torch.cuda.is_available() else 'cpu')
        
    def build_model(self) -> None:
        """Build the simple neural network."""
        hyperparams = self.config.get('hyperparameters', {})
        hidden_layers = hyperparams.get('hidden_layers', [64, 32])
        activation = hyperparams.get('activation', 'relu')
        dropout = hyperparams.get('dropout', 0.2)
        
        # Assuming 3 input features (function call counts) and 2 outputs (CPU, memory)
        input_size = 3
        output_size = 2
        
        layers = []
        prev_size = input_size
        
        # Add hidden layers
        for hidden_size in hidden_layers:
            layers.append(nn.Linear(prev_size, hidden_size))
            if activation == 'relu':
                layers.append(nn.ReLU())
            elif activation == 'tanh':
                layers.append(nn.Tanh())
            elif activation == 'sigmoid':
                layers.append(nn.Sigmoid())
            layers.append(nn.Dropout(dropout))
            prev_size = hidden_size
        
        # Output layer
        layers.append(nn.Linear(prev_size, output_size))
        
        self.model = nn.Sequential(*layers).to(self.device)
        
    def train(self, X_train: np.ndarray, y_train: np.ndarray, 
              X_val: Optional[np.ndarray] = None, y_val: Optional[np.ndarray] = None) -> Dict[str, List[float]]:
        """Train the neural network."""
        if self.model is None:
            self.build_model()
            
        hyperparams = self.config.get('hyperparameters', {})
        learning_rate = hyperparams.get('learning_rate', 0.001)
        epochs = hyperparams.get('epochs', 100)
        batch_size = hyperparams.get('batch_size', 32)
        
        # Convert to PyTorch tensors
        X_train_tensor = torch.FloatTensor(X_train).to(self.device)
        y_train_tensor = torch.FloatTensor(y_train).to(self.device)
        
        # Create data loader
        train_dataset = TensorDataset(X_train_tensor, y_train_tensor)
        train_loader = DataLoader(train_dataset, batch_size=batch_size, shuffle=True)
        
        # Validation data
        if X_val is not None and y_val is not None:
            X_val_tensor = torch.FloatTensor(X_val).to(self.device)
            y_val_tensor = torch.FloatTensor(y_val).to(self.device)
        
        # Loss function and optimizer
        criterion = nn.MSELoss()
        optimizer = optim.Adam(self.model.parameters(), lr=learning_rate)
        
        # Training history
        history = {
            'train_loss': [],
            'train_mse': [],
            'val_loss': [],
            'val_mse': []
        }
        
        # Training loop
        for epoch in range(epochs):
            self.model.train()
            epoch_loss = 0.0
            
            for batch_X, batch_y in train_loader:
                optimizer.zero_grad()
                outputs = self.model(batch_X)
                loss = criterion(outputs, batch_y)
                loss.backward()
                optimizer.step()
                epoch_loss += loss.item()
            
            avg_train_loss = epoch_loss / len(train_loader)
            history['train_loss'].append(avg_train_loss)
            
            # Calculate training MSE
            self.model.eval()
            with torch.no_grad():
                train_pred = self.model(X_train_tensor)
                train_mse = criterion(train_pred, y_train_tensor).item()
                history['train_mse'].append(train_mse)
                
                # Validation metrics
                if X_val is not None and y_val is not None:
                    val_pred = self.model(X_val_tensor)
                    val_loss = criterion(val_pred, y_val_tensor).item()
                    history['val_loss'].append(val_loss)
                    history['val_mse'].append(val_loss)  # MSE = loss for this case
        
        self.is_trained = True
        self.training_history = history
        return history
        
    def predict(self, X: np.ndarray) -> np.ndarray:
        """Make predictions with the trained model."""
        if not self.is_trained or self.model is None:
            raise ValueError("Model must be trained before making predictions")
            
        self.model.eval()
        with torch.no_grad():
            X_tensor = torch.FloatTensor(X).to(self.device)
            predictions = self.model(X_tensor)
            return predictions.cpu().numpy()
    
    def _save_model_specific(self, save_path: Path) -> None:
        """Save the PyTorch model."""
        torch.save(self.model.state_dict(), save_path / 'model.pth')
    
    def _load_model_specific(self, load_path: Path) -> None:
        """Load the PyTorch model."""
        self.build_model()  # Rebuild architecture first
        self.model.load_state_dict(torch.load(load_path / 'model.pth', map_location=self.device))


class DeepNeuralNetwork(BaseModel):
    """Deep feedforward neural network for resource prediction."""
    
    def __init__(self, config: Dict[str, Any]):
        super().__init__("DeepNeuralNetwork", config)
        self.model = None
        self.device = torch.device('cuda' if torch.cuda.is_available() else 'cpu')
        
    def build_model(self) -> None:
        """Build the deep neural network."""
        hyperparams = self.config.get('hyperparameters', {})
        hidden_layers = hyperparams.get('hidden_layers', [128, 64, 32, 16])
        activation = hyperparams.get('activation', 'relu')
        dropout = hyperparams.get('dropout', 0.3)
        
        input_size = 3
        output_size = 2
        
        layers = []
        prev_size = input_size
        
        # Add hidden layers with batch normalization
        for i, hidden_size in enumerate(hidden_layers):
            layers.append(nn.Linear(prev_size, hidden_size))
            layers.append(nn.BatchNorm1d(hidden_size))
            if activation == 'relu':
                layers.append(nn.ReLU())
            elif activation == 'tanh':
                layers.append(nn.Tanh())
            elif activation == 'sigmoid':
                layers.append(nn.Sigmoid())
            layers.append(nn.Dropout(dropout))
            prev_size = hidden_size
        
        # Output layer
        layers.append(nn.Linear(prev_size, output_size))
        
        self.model = nn.Sequential(*layers).to(self.device)
        
    def train(self, X_train: np.ndarray, y_train: np.ndarray, 
              X_val: Optional[np.ndarray] = None, y_val: Optional[np.ndarray] = None) -> Dict[str, List[float]]:
        """Train the deep neural network."""
        if self.model is None:
            self.build_model()
            
        hyperparams = self.config.get('hyperparameters', {})
        learning_rate = hyperparams.get('learning_rate', 0.0001)
        epochs = hyperparams.get('epochs', 200)
        batch_size = hyperparams.get('batch_size', 32)
        
        # Convert to PyTorch tensors
        X_train_tensor = torch.FloatTensor(X_train).to(self.device)
        y_train_tensor = torch.FloatTensor(y_train).to(self.device)
        
        # Create data loader
        train_dataset = TensorDataset(X_train_tensor, y_train_tensor)
        train_loader = DataLoader(train_dataset, batch_size=batch_size, shuffle=True)
        
        # Validation data
        if X_val is not None and y_val is not None:
            X_val_tensor = torch.FloatTensor(X_val).to(self.device)
            y_val_tensor = torch.FloatTensor(y_val).to(self.device)
        
        # Loss function and optimizer with learning rate scheduling
        criterion = nn.MSELoss()
        optimizer = optim.Adam(self.model.parameters(), lr=learning_rate)
        scheduler = optim.lr_scheduler.ReduceLROnPlateau(optimizer, patience=10, factor=0.5)
        
        # Training history
        history = {
            'train_loss': [],
            'train_mse': [],
            'val_loss': [],
            'val_mse': []
        }
        
        # Training loop
        for epoch in range(epochs):
            self.model.train()
            epoch_loss = 0.0
            
            for batch_X, batch_y in train_loader:
                optimizer.zero_grad()
                outputs = self.model(batch_X)
                loss = criterion(outputs, batch_y)
                loss.backward()
                optimizer.step()
                epoch_loss += loss.item()
            
            avg_train_loss = epoch_loss / len(train_loader)
            history['train_loss'].append(avg_train_loss)
            
            # Calculate training MSE
            self.model.eval()
            with torch.no_grad():
                train_pred = self.model(X_train_tensor)
                train_mse = criterion(train_pred, y_train_tensor).item()
                history['train_mse'].append(train_mse)
                
                # Validation metrics
                if X_val is not None and y_val is not None:
                    val_pred = self.model(X_val_tensor)
                    val_loss = criterion(val_pred, y_val_tensor).item()
                    history['val_loss'].append(val_loss)
                    history['val_mse'].append(val_loss)
                    
                    # Update learning rate
                    scheduler.step(val_loss)
        
        self.is_trained = True
        self.training_history = history
        return history
        
    def predict(self, X: np.ndarray) -> np.ndarray:
        """Make predictions with the trained model."""
        if not self.is_trained or self.model is None:
            raise ValueError("Model must be trained before making predictions")
            
        self.model.eval()
        with torch.no_grad():
            X_tensor = torch.FloatTensor(X).to(self.device)
            predictions = self.model(X_tensor)
            return predictions.cpu().numpy()
    
    def _save_model_specific(self, save_path: Path) -> None:
        """Save the PyTorch model."""
        torch.save(self.model.state_dict(), save_path / 'model.pth')
    
    def _load_model_specific(self, load_path: Path) -> None:
        """Load the PyTorch model."""
        self.build_model()  # Rebuild architecture first
        self.model.load_state_dict(torch.load(load_path / 'model.pth', map_location=self.device))