"use client";
import { useState } from "react";
import InputField from "@/components/InputField/InputField";
import styles from "../page.module.css";
import { Ubuntu_Mono } from "next/font/google";
import SecondaryButton from "@/components/Buttons/SecondaryButton";
import PrimaryButton from "@/components/Buttons/PrimaryButton";
import { useRouter } from "next/navigation";
const ubuntuMono = Ubuntu_Mono({
  weight: "400",
  style: "normal",
  subsets: ["latin"],
});

export default function Home() {
  const [registryUrl, setRegistryUrl] = useState("");
  const router = useRouter();
  const handleRegistryUrlChange = (event) => {
    setRegistryUrl(event.target.value);
  };
  const handleContinue = async () => {
    try {
      const response = await fetch("/api/get-network-participant", {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
        },
        body: JSON.stringify({ registryUrl }),
      });

      if (response.ok) {
        const networkParticipantsData = await response.json();
        router.push("/monitor/dashboard");
        // redirecting to the dashboard page
      } else {
        console.log("error fetching participant data");
      }
    } catch (error) {
      console.error("Error:", error);
    }
  };

  return (
    <>
      <main className={ubuntuMono.className}>
        <div className={styles.mainContainer}>
          <button
            onClick={() => window.history.back()}
            className={styles.backButton}
          >
            Back
          </button>
          {/* <p className={styles.currentRoute}>ONIX{pathname}</p> */}
          <p className={styles.mainText}>Network Registry</p>
          <div className={styles.formContainer}>
            <InputField
              value={registryUrl}
              onChange={handleRegistryUrlChange}
              label={"Registry URL"}
            />
            {/* <InputField label={"Subscriber Id"} /> */}
            {/* <InputField label={"UniqueKey Id"} /> */}
            {/* <InputField label={"Network configuration URL"} /> */}

            <div className={styles.buttonsContainer}>
              {/* <SecondaryButton text={"Cancel"} /> */}
              <PrimaryButton onClick={handleContinue} text={"Continue"} />
            </div>
          </div>
        </div>
      </main>
    </>
  );
}
