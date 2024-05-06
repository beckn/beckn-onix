import React from "react";
import styles from "./Buttons.module.css";

const PrimaryButton = ({ label = "continue", onClick, disabled = false }) => {
  return (
    <button
      className={styles.primaryButton}
      onClick={onClick}
      disabled={disabled}
    >
      {label}
    </button>
  );
};

export default PrimaryButton;
