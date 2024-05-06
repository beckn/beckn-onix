import React from "react";
import styles from "./Slider.module.css";

const Slider = ({ label, checked, toggleChecked }) => {
  return (
    <div className={styles.inputContainer}>
      <label className={styles.inputLabel}>{label}</label>
      <label className={styles.switch}>
        <input
          type="checkbox"
          checked={checked}
          onChange={() => toggleChecked(!checked)}
        />
        <span className={`${styles.slider} ${styles.round}`}></span>
      </label>
    </div>
  );
};

export default Slider;
