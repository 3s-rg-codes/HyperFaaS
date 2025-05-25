"""
Model factory for creating different types of resource prediction models.
Provides a unified interface to instantiate and manage all model types.
"""

import importlib
import sys
from pathlib import Path
from typing import Dict, Any, Type
import yaml

# Add model paths to sys.path
current_dir = Path(__file__).parent.parent.parent
sys.path.append(str(current_dir / "models" / "regression"))
sys.path.append(str(current_dir / "models" / "neural"))
sys.path.append(str(current_dir / "models" / "ensemble"))

from base_model import BaseModel
from linear_models import LinearRegressionModel, PolynomialRegressionModel, RidgeRegressionModel, LassoRegressionModel
from neural_networks import SimpleNeuralNetwork, DeepNeuralNetwork
from tree_models import RandomForestModel, GradientBoostingModel


class ModelFactory:
    """Factory class for creating and managing ML models."""
    
    # Registry of available models
    MODEL_REGISTRY = {
        'linear_regression': LinearRegressionModel,
        'polynomial_regression': PolynomialRegressionModel,
        'ridge_regression': RidgeRegressionModel,
        'lasso_regression': LassoRegressionModel,
        'simple_neural_network': SimpleNeuralNetwork,
        'deep_neural_network': DeepNeuralNetwork,
        'random_forest': RandomForestModel,
        'gradient_boosting': GradientBoostingModel,
    }
    
    @classmethod
    def create_model(cls, model_name: str, config: Dict[str, Any]) -> BaseModel:
        """
        Create a model instance based on name and configuration.
        
        Args:
            model_name: Name of the model to create
            config: Model configuration dictionary
            
        Returns:
            Instance of the requested model
            
        Raises:
            ValueError: If model name is not registered
        """
        if model_name not in cls.MODEL_REGISTRY:
            available_models = list(cls.MODEL_REGISTRY.keys())
            raise ValueError(f"Unknown model '{model_name}'. Available models: {available_models}")
        
        model_class = cls.MODEL_REGISTRY[model_name]
        return model_class(config)
    
    @classmethod
    def get_available_models(cls) -> list:
        """Get list of available model names."""
        return list(cls.MODEL_REGISTRY.keys())
    
    @classmethod
    def get_models_by_type(cls, model_type: str) -> list:
        """
        Get available models filtered by type.
        
        Args:
            model_type: Type of models ('regression', 'neural', 'ensemble')
            
        Returns:
            List of model names of the specified type
        """
        type_mapping = {
            'regression': ['linear_regression', 'polynomial_regression', 'ridge_regression', 'lasso_regression'],
            'neural': ['simple_neural_network', 'deep_neural_network'],
            'ensemble': ['random_forest', 'gradient_boosting']
        }
        
        return type_mapping.get(model_type, [])
    
    @classmethod
    def create_models_from_config(cls, config_path: str) -> Dict[str, BaseModel]:
        """
        Create multiple models from a YAML configuration file.
        
        Args:
            config_path: Path to the YAML configuration file
            
        Returns:
            Dictionary mapping model names to model instances
        """
        with open(config_path, 'r') as f:
            config = yaml.safe_load(f)
        
        models = {}
        model_configs = config.get('models', {})
        
        for model_name, model_config in model_configs.items():
            try:
                model = cls.create_model(model_name, model_config)
                models[model_name] = model
                print(f"✓ Created model: {model_name}")
            except Exception as e:
                print(f"✗ Failed to create model {model_name}: {str(e)}")
        
        return models
    
    @classmethod
    def get_model_info(cls, model_name: str) -> Dict[str, Any]:
        """
        Get information about a specific model.
        
        Args:
            model_name: Name of the model
            
        Returns:
            Dictionary with model information
        """
        if model_name not in cls.MODEL_REGISTRY:
            return {}
        
        model_class = cls.MODEL_REGISTRY[model_name]
        
        # Determine model type
        model_type = "unknown"
        if model_name in cls.get_models_by_type('regression'):
            model_type = "regression"
        elif model_name in cls.get_models_by_type('neural'):
            model_type = "neural"
        elif model_name in cls.get_models_by_type('ensemble'):
            model_type = "ensemble"
        
        return {
            'name': model_name,
            'class': model_class.__name__,
            'type': model_type,
            'module': model_class.__module__,
            'doc': model_class.__doc__ or "No documentation available"
        }
    
    @classmethod
    def validate_config(cls, model_name: str, config: Dict[str, Any]) -> bool:
        """
        Validate model configuration.
        
        Args:
            model_name: Name of the model
            config: Configuration to validate
            
        Returns:
            True if configuration is valid
        """
        try:
            # Try to create the model to validate config
            model = cls.create_model(model_name, config)
            model.build_model()
            return True
        except Exception as e:
            print(f"Configuration validation failed for {model_name}: {str(e)}")
            return False


def print_model_summary():
    """Print a summary of all available models."""
    print("🤖 Available HyperFaaS Resource Prediction Models")
    print("=" * 60)
    
    for model_type in ['regression', 'neural', 'ensemble']:
        models = ModelFactory.get_models_by_type(model_type)
        print(f"\n📊 {model_type.upper()} MODELS:")
        print("-" * 30)
        
        for model_name in models:
            info = ModelFactory.get_model_info(model_name)
            print(f"  • {model_name}")
            print(f"    Class: {info['class']}")
            print(f"    Description: {info['doc'][:60]}...")
            print()


if __name__ == "__main__":
    print_model_summary() 